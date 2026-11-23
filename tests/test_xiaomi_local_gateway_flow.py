"""Headless tests for QR onboarding and account reauthentication."""

import unittest
from types import SimpleNamespace
from unittest.mock import AsyncMock, patch

from legacy.custom_components.xiaomi_local_gateway import config_flow
from custom_components.xiaomi_direct_pilot.xiaomi_direct.account import AccountChallenge, AccountError


class Session:
    def __init__(self):
        self.closed = False

    async def close(self):
        self.closed = True

    async def __aenter__(self):
        return self

    async def __aexit__(self, *_exc):
        await self.close()

    def __getattr__(self, name):
        # The real reauthentication path passes the session as the account
        # client; the test only needs its lifecycle and identity.
        async def unused(*_args, **_kwargs):
            raise AssertionError(f"Unexpected session operation: {name}")
        return unused


class LocalGatewayFlowTest(unittest.IsolatedAsyncioTestCase):
    def flow(self, **attrs):
        flow = object.__new__(config_flow.XiaomiLocalGatewayOptionsFlow)
        entry = SimpleNamespace(options={}, runtime_data=None)
        flow.hass = SimpleNamespace(
            async_add_executor_job=AsyncMock(return_value={}),
            config_entries=SimpleNamespace(async_get_known_entry=lambda _entry_id: entry),
        )
        flow.handler = "test-entry"
        flow._config_entry = entry
        flow.async_show_form = lambda **kw: {"type": "form", **kw}
        flow.async_create_entry = lambda **kw: {"type": "create_entry", **kw}
        flow.async_abort = lambda **kw: {"type": "abort", **kw}
        for key, value in attrs.items():
            setattr(flow, key, value)
        return flow

    async def test_qr_waits_for_approval_then_shows_camera_selection(self):
        flow = self.flow(_gateway_camera_choices={})
        client = Session()
        approval = SimpleNamespace(image="https://example.invalid/qr.png", page="https://example.invalid/")
        camera = {"device_id": "mock-c201", "name": "Mock C201", "model": "xiaomi.camera.c201"}
        account = SimpleNamespace(user_id="mock-user", pass_token="mock-token")
        async def load_inventory():
            self.assertFalse(client.closed, "QR session must remain open until inventory loads")
            return {"list": [{
                "did": "mock-c201", "name": "Mock C201", "model": "xiaomi.camera.c201",
            }]}
        account.devices = AsyncMock(side_effect=load_inventory)
        flow._region = "de"
        start = {"method": "qr", "region": "de"}

        async def resolve(_hass, _inventory):
            return [camera]

        with patch("custom_components.xiaomi_direct_pilot.xiaomi_direct.account.create_client_session", return_value=client), \
             patch("custom_components.xiaomi_direct_pilot.xiaomi_direct.account.XiaomiAccount.start_qr", AsyncMock(return_value=approval)) as start_qr, \
             patch("custom_components.xiaomi_direct_pilot.xiaomi_direct.account.XiaomiAccount.finish_qr", side_effect=[AccountChallenge("qr_pending"), account]) as finish_qr, \
             patch("custom_components.xiaomi_direct_pilot.inventory.resolve_cameras", resolve):
            form = await flow.async_step_direct_signin(start)
            self.assertEqual(form["step_id"], "direct_qr")
            self.assertIs(flow._qr_client, client)
            pending = await flow.async_step_direct_qr({})
            self.assertEqual(pending["errors"], {"base": "qr_pending"})
            self.assertFalse(client.closed)
            selected = await flow.async_step_direct_qr({})
            self.assertEqual(selected["step_id"], "direct_cameras")
            self.assertTrue(client.closed)
            saved = await flow.async_step_direct_cameras({"camera_ids": ["mock-c201"]})

        self.assertEqual(saved["type"], "create_entry")
        self.assertEqual(saved["data"]["direct_account_user_id"], "mock-user")
        self.assertEqual(saved["data"]["direct_account_pass_token"], "mock-token")
        self.assertEqual(start_qr.await_count, 1)
        self.assertEqual(finish_qr.await_count, 2)
        self.assertEqual(account.devices.await_count, 1)

    async def test_reauth_verification_loads_inventory_before_session_closes(self):
        entry = SimpleNamespace(options={"direct_account_user_id": "original-user"})
        updated = {"type": "abort", "reason": "reauth_successful"}
        flow = object.__new__(config_flow.XiaomiLocalGatewayConfigFlow)
        flow._get_reauth_entry = lambda: entry
        flow.async_show_form = lambda **kw: {"type": "form", **kw}
        flow.async_update_reload_and_abort = lambda _entry, **kw: updated.update(kw) or updated
        session = Session()

        class Account:
            user_id = "original-user"
            pass_token = "new-token"

            async def devices(self):
                if session.closed:
                    raise AssertionError("inventory requested after session closed")
                return {"list": []}

        account = Account()
        flow._reauth_verification = AsyncMock(return_value=account)
        with patch("custom_components.xiaomi_direct_pilot.xiaomi_direct.account.create_client_session", return_value=session):
            result = await flow.async_step_reauth_verify({"code": " 123456 "})

        self.assertIs(result, updated)
        self.assertTrue(session.closed)
        self.assertEqual(updated["options"]["direct_account_pass_token"], "new-token")
        self.assertEqual(flow._reauth_verification.await_args.args[1], "123456")

    async def test_new_entity_choice_removes_saved_mapping(self):
        flow = self.flow(_gateway_camera_choices={"old-id": "Old camera"})
        flow._direct_account = SimpleNamespace(user_id="user", pass_token="token")
        flow._region = "de"
        flow._direct_cameras = [{"device_id": "did-1", "name": "Camera", "model": "xiaomi.camera.c201"}]
        flow.config_entry.options["direct_camera_id_map"] = {"did-1": "old-id", "other-did": "keep-id"}

        result = await flow.async_step_direct_cameras({"camera_ids": ["did-1"], "map_did-1": ""})

        self.assertEqual(result["type"], "create_entry")
        self.assertEqual(result["data"]["direct_camera_id_map"], {"other-did": "keep-id"})

    async def test_reauth_qr_inventory_error_closes_session_and_restarts_login(self):
        entry = SimpleNamespace(options={"direct_account_user_id": "original-user"})
        flow = object.__new__(config_flow.XiaomiLocalGatewayConfigFlow)
        flow._get_reauth_entry = lambda: entry
        flow.async_show_form = lambda **kw: {"type": "form", **kw}
        flow._region = "de"
        flow._approval = SimpleNamespace(image="https://example.invalid/qr.png", page="https://example.invalid/")
        flow._reauth_client = Session()
        account = SimpleNamespace(devices=AsyncMock(side_effect=AccountError("inventory unavailable")))
        with patch("custom_components.xiaomi_direct_pilot.xiaomi_direct.account.XiaomiAccount.finish_qr",
                   AsyncMock(return_value=account)):
            result = await flow.async_step_reauth_qr({})

        self.assertEqual(result["step_id"], "reauth_confirm")
        self.assertIsNone(flow._reauth_client)
        self.assertEqual(result["errors"], {"base": "inventory_unavailable"})

    async def test_timeout_finishing_direct_qr_closes_and_requests_new_login(self):
        flow = self.flow()
        flow._region = "de"
        flow._approval = SimpleNamespace(image="qr", page="page")
        flow._qr_client = Session()
        with patch("custom_components.xiaomi_direct_pilot.xiaomi_direct.account.XiaomiAccount.finish_qr",
                   AsyncMock(side_effect=TimeoutError)):
            result = await flow.async_step_direct_qr({})
        self.assertEqual(result["step_id"], "direct_signin")
        self.assertEqual(result["errors"], {"base": "cannot_sign_in"})
        self.assertIsNone(flow._qr_client)

    async def test_timeout_finishing_reauth_qr_closes_and_requests_new_login(self):
        entry = SimpleNamespace(options={"direct_account_user_id": "original-user"})
        flow = object.__new__(config_flow.XiaomiLocalGatewayConfigFlow)
        flow._get_reauth_entry = lambda: entry
        flow.async_show_form = lambda **kw: {"type": "form", **kw}
        flow._region = "de"
        flow._approval = SimpleNamespace(image="qr", page="page")
        flow._reauth_client = Session()
        with patch("custom_components.xiaomi_direct_pilot.xiaomi_direct.account.XiaomiAccount.finish_qr",
                   AsyncMock(side_effect=TimeoutError)):
            result = await flow.async_step_reauth_qr({})
        self.assertEqual(result["step_id"], "reauth_confirm")
        self.assertEqual(result["errors"], {"base": "cannot_sign_in"})
        self.assertIsNone(flow._reauth_client)

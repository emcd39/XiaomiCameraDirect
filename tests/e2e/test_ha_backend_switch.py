"""Exercise backend switching on one disposable HA config entry."""

from importlib import import_module
import logging
import os
from pathlib import Path
import shutil
import tempfile
import unittest
from types import SimpleNamespace
from unittest.mock import AsyncMock, patch

from aiohttp.resolver import ThreadedResolver
from homeassistant.bootstrap import async_setup_hass
from homeassistant.components.camera.helper import get_camera_from_entity_id
from homeassistant.helpers import aiohttp_client, device_registry, entity_registry
from homeassistant.runner import RuntimeConfig

import custom_components
from custom_components.xiaomi_direct_pilot.xiaomi_direct import account as xiaomi_account


class HATestResolver(ThreadedResolver):
    async def real_close(self):
        await self.close()


class DirectFake:
    def __init__(self):
        self.closed = AsyncMock()
        self.stream_requested = AsyncMock(return_value="http://127.0.0.1:8123/direct-c201.m3u8")

    async def cameras(self):
        return [{
            "id": "mock-c201", "name": "C201", "model": "xiaomi.camera.c201",
            "ptz": True, "on_demand_stream": True, "available": True,
        }]

    async def camera_stream(self, camera_id):
        return await self.stream_requested(camera_id)

    async def ptz_step(self, camera_id, direction, *, managed=False):
        return None

    async def camera_read_capabilities(self):
        return set()

    async def camera_settings(self, camera_id):
        raise AssertionError("Direct backend has no settings endpoint")

    async def camera_storage(self, camera_id):
        raise AssertionError("Direct backend has no storage endpoint")

    async def close(self):
        await self.closed()


class QRSession:
    def __init__(self):
        self.closed = False

    async def close(self):
        self.closed = True


class LocalMediaFake:
    def __init__(self, *_args, **_kwargs):
        self.url = None
        self.closed = AsyncMock()

    async def start(self):
        self.url = "http://127.0.0.1:8123/direct-c201.m3u8"

    async def close(self):
        await self.closed()


class PTZFake:
    def __init__(self, *_args):
        self.press_call = AsyncMock()

    async def press(self, direction):
        await self.press_call(direction)


class HABackendSwitchTest(unittest.IsolatedAsyncioTestCase):
    async def test_switch_preserves_entity_and_device_ids_and_gateway_rollback(self):
        camera = {
            "id": "mock-c201", "name": "C201", "model": "xiaomi.camera.c201",
            "on_demand_stream": True, "ptz": True,
        }
        gateway_url = "rtsp://127.0.0.1:8554/c201"
        runtime_client = QRSession()
        media_sources = []

        def media_factory(*args, **kwargs):
            media = LocalMediaFake(*args, **kwargs)
            media_sources.append(media)
            return media

        with tempfile.TemporaryDirectory() as temporary:
            config = Path(temporary)
            root = Path(__file__).parents[2]
            shutil.copytree(
                root / "legacy/custom_components/xiaomi_local_gateway",
                config / "custom_components/xiaomi_local_gateway",
            )
            shutil.copytree(
                root / "custom_components/xiaomi_direct_pilot",
                config / "custom_components/xiaomi_direct_pilot",
            )
            # The test process has already imported custom_components from the
            # checkout. Include the disposable config's manually installed adapter.
            integration_path = patch.object(
                custom_components, "__path__",
                [*custom_components.__path__, str(config / "custom_components")],
            )
            integration_path.start()
            self.addCleanup(integration_path.stop)
            resolver = patch.object(
                aiohttp_client, "_async_make_resolver", side_effect=lambda _hass: HATestResolver(),
            )
            resolver.start()
            self.addCleanup(resolver.stop)
            (config / "configuration.yaml").write_text("stream:\n  segment_duration: 2\n", encoding="utf-8")

            root_logger = logging.getLogger()
            previous_handlers = {
                handler
                for logger in logging.root.manager.loggerDict.values()
                if isinstance(logger, logging.Logger)
                for handler in logger.handlers
            } | set(root_logger.handlers)
            hass = None
            try:
                with patch.dict(os.environ, {"HA_DISABLE_LOG_FILE": "1"}):
                    hass = await async_setup_hass(RuntimeConfig(str(config), skip_pip=True))
                self.assertIsNotNone(hass)
                await hass.async_start()
                await hass.async_block_till_done()
                gateway_api = import_module("custom_components.xiaomi_local_gateway.api")
                with (
                    patch.object(gateway_api.GatewayClient, "cameras", AsyncMock(return_value=[camera])),
                    patch.object(gateway_api.GatewayClient, "camera_stream", AsyncMock(return_value=gateway_url)) as gateway_stream,
                    patch.object(gateway_api.GatewayClient, "ptz_step", AsyncMock()) as gateway_ptz,
                    patch.object(gateway_api.GatewayClient, "camera_read_capabilities", AsyncMock(return_value=set())),
                ):
                    flow = hass.config_entries.flow
                    form = await flow.async_init("xiaomi_local_gateway", context={"source": "user"})
                    result = await flow.async_configure(form["flow_id"], {
                        "url": "http://gateway.invalid:8099", "token": "test-token",
                    })
                    self.assertEqual(result["type"], "create_entry")
                    entry = result["result"]
                    await hass.async_block_till_done()
                    self.assertEqual(entry.data, {"url": "http://gateway.invalid:8099", "token": "test-token"})

                    entities = entity_registry.async_get(hass)
                    devices = device_registry.async_get(hass)

                    def registered():
                        return {
                            (item.domain, item.unique_id): (item.entity_id, item.device_id)
                            for item in entity_registry.async_entries_for_config_entry(entities, entry.entry_id)
                        }

                    original = registered()
                    self.assertIn(("camera", "mock-c201_camera"), original)
                    self.assertTrue(any(domain == "button" and unique.startswith("mock-c201_ptz_")
                                        for domain, unique in original))
                    camera_entity_id, camera_device_id = original[("camera", "mock-c201_camera")]
                    identifiers_before = devices.async_get(camera_device_id).identifiers
                    self.assertIn(("xiaomi_local_gateway", "mock-c201"), identifiers_before)

                    qr_session = QRSession()
                    approval = SimpleNamespace(image="https://example.invalid/qr.png", page="https://example.invalid/")
                    direct_account = SimpleNamespace(user_id="mock-user", pass_token="mock-pass")

                    async def account_devices():
                        self.assertFalse(qr_session.closed, "QR session must remain open through inventory")
                        return {"list": [{
                            "did": "different-xiaomi-did", "name": "C201", "model": "xiaomi.camera.c201",
                        }]}

                    inventory_loads = 0

                    async def devices_for_account():
                        nonlocal inventory_loads
                        inventory_loads += 1
                        if inventory_loads == 1:
                            return await account_devices()
                        return {"list": [{
                            "did": "different-xiaomi-did", "name": "C201", "model": "xiaomi.camera.c201",
                            "localip": "192.168.2.11",
                        }]}

                    direct_account.devices = AsyncMock(side_effect=devices_for_account)

                    options = hass.config_entries.options
                    form = await options.async_init(entry.entry_id)
                    selected = await options.async_configure(form["flow_id"], {"backend": "direct"})
                    self.assertEqual(selected["step_id"], "direct_signin")
                    with (
                        patch.object(xiaomi_account.XiaomiAccount, "start_qr", AsyncMock(return_value=approval)),
                        patch.object(xiaomi_account.XiaomiAccount, "finish_qr", AsyncMock(return_value=direct_account)),
                        patch.object(xiaomi_account, "create_client_session", side_effect=[qr_session, runtime_client]),
                        patch("custom_components.xiaomi_direct_pilot.inventory.create_account", return_value=direct_account),
                        patch("custom_components.xiaomi_direct_pilot.inventory.resolve_cameras", AsyncMock(return_value=[{
                            "device_id": "different-xiaomi-did", "name": "C201", "model": "xiaomi.camera.c201",
                            "camera_ip": "192.168.2.11", "source_ip": "192.168.2.10",
                            "camera_lan": "192.168.2.0/24",
                        }])),
                        patch("custom_components.xiaomi_direct_pilot.camera._video_opener", return_value=object()),
                        patch("custom_components.xiaomi_direct_pilot.xiaomi_direct.ha_media.LocalMediaSource",
                              side_effect=media_factory),
                        patch("custom_components.xiaomi_direct_pilot.button.PTZControl", PTZFake),
                    ):
                        qr = await options.async_configure(selected["flow_id"], {"region": "de", "method": "qr"})
                        self.assertEqual(qr["step_id"], "direct_qr")
                        cameras = await options.async_configure(qr["flow_id"], {})
                        self.assertEqual(cameras["step_id"], "direct_cameras")
                        result = await options.async_configure(cameras["flow_id"], {
                            "camera_ids": ["different-xiaomi-did"], "map_different-xiaomi-did": "mock-c201",
                        })
                    self.assertEqual(result["type"], "create_entry")
                    self.assertTrue(qr_session.closed)
                    self.assertEqual(entry.options["direct_camera_id_map"], {"different-xiaomi-did": "mock-c201"})
                    await hass.async_block_till_done()
                    active_direct = entry.runtime_data.client
                    direct_entity = get_camera_from_entity_id(hass, camera_entity_id)
                    self.assertTrue(direct_entity.available)
                    self.assertEqual(await direct_entity.stream_source(), "http://127.0.0.1:8123/direct-c201.m3u8")
                    self.assertEqual(await direct_entity.coordinator.client.camera_stream("mock-c201"),
                                     "http://127.0.0.1:8123/direct-c201.m3u8")
                    self.assertEqual(registered(), original)
                    self.assertEqual(devices.async_get(camera_device_id).identifiers, identifiers_before)
                    self.assertEqual(len(registered()), len(original))
                    await active_direct.ptz_step("mock-c201", "left")

                    options = hass.config_entries.options
                    form = await options.async_init(entry.entry_id)
                    self.assertEqual(form["step_id"], "init")
                    result = await options.async_configure(form["flow_id"], {"backend": "gateway"})
                    self.assertEqual(result["type"], "create_entry")
                    await hass.async_block_till_done()
                    self.assertEqual(entry.data, {"url": "http://gateway.invalid:8099", "token": "test-token"})
                    self.assertEqual(entry.options["direct_account_pass_token"], "mock-pass")
                    self.assertEqual(registered(), original)
                    self.assertTrue(runtime_client.closed)
                    self.assertEqual(media_sources[0].closed.await_count, 1)
                    gateway_entity = get_camera_from_entity_id(hass, camera_entity_id)
                    self.assertEqual(await gateway_entity.stream_source(), gateway_url)
                    gateway_stream.assert_awaited_once_with("mock-c201")
                    await gateway_entity.coordinator.client.ptz_step("mock-c201", "left", managed=False)
                    gateway_ptz.assert_awaited_once_with("mock-c201", "left", managed=False)
            finally:
                if hass is not None:
                    await hass.async_stop()
                for handler in set(root_logger.handlers) - previous_handlers:
                    root_logger.removeHandler(handler)
                for logger in logging.root.manager.loggerDict.values():
                    if not isinstance(logger, logging.Logger):
                        continue
                    for handler in tuple(logger.handlers):
                        filename = getattr(handler, "baseFilename", "")
                        if handler not in previous_handlers or filename.startswith(str(config)):
                            logger.removeHandler(handler)
                            handler.close()


if __name__ == "__main__":
    unittest.main()

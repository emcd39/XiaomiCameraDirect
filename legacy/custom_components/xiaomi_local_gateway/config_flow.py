"""UI onboarding for a local Gateway URL and API token."""
from __future__ import annotations
import logging
import voluptuous as vol
from homeassistant.config_entries import ConfigFlow, OptionsFlowWithReload
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from homeassistant.helpers import config_validation as cv
from homeassistant.helpers.selector import TextSelector, TextSelectorConfig, TextSelectorType
from .api import GatewayClient, GatewayError
from .const import CONF_TOKEN, CONF_URL, DOMAIN, NAME

LOGGER = logging.getLogger(__name__)
_REGIONS = ("de", "cn", "i2", "ru", "sg", "us")
_PASSWORD = TextSelector(TextSelectorConfig(type=TextSelectorType.PASSWORD))
_AUTH_FIELDS = {
    vol.Required("method", default="qr"): vol.In({"qr": "Xiaomi Home QR", "password": "Xiaomi account password"}),
    vol.Optional("username"): str,
    vol.Optional("password"): _PASSWORD,
}

class XiaomiLocalGatewayConfigFlow(ConfigFlow, domain=DOMAIN):
    VERSION = 1

    @staticmethod
    def async_get_options_flow(config_entry):
        return XiaomiLocalGatewayOptionsFlow()

    async def async_step_reauth(self, entry_data):
        entry = self._get_reauth_entry()
        self._region = entry.options.get("direct_region", "de")
        return await self.async_step_reauth_confirm()

    async def async_step_reauth_confirm(self, user_input=None):
        errors = {}
        if user_input is not None:
            from custom_components.xiaomi_direct_pilot.xiaomi_direct.account import (
                AccountChallenge, AccountError, XiaomiAccount, create_client_session,
            )

            try:
                if user_input["method"] == "qr":
                    self._reauth_client = create_client_session()
                    self._approval = await XiaomiAccount.start_qr(self._reauth_client)
                    return await self.async_step_reauth_qr()
                async with create_client_session() as client:
                    if not user_input.get("username", "").strip() or not user_input.get("password"):
                        errors["base"] = "credentials_required"
                    else:
                        account = await XiaomiAccount.login(
                            client, user_input["username"].strip(), user_input["password"], self._region,
                        )
                        return await self._accept_reauth_account(account)
            except AccountChallenge as error:
                if getattr(self, "_reauth_client", None) is not None:
                    await self._close_reauth_qr()
                if error.code == "verification_required" and error.continuation:
                    self._reauth_verification = error.continuation
                    return await self.async_step_reauth_verify()
                errors["base"] = error.code
            except AccountError as error:
                await self._close_reauth_qr()
                LOGGER.warning("Xiaomi direct reauthentication failed: %s", type(error).__name__)
                errors["base"] = "cannot_sign_in"
            except Exception as error:
                await self._close_reauth_qr()
                LOGGER.warning("Xiaomi QR challenge failed: %s", type(error).__name__)
                errors["base"] = "cannot_sign_in"
        if getattr(self, "_reauth_error", None):
            errors["base"] = self._reauth_error
            self._reauth_error = None
        return self.async_show_form(step_id="reauth_confirm", data_schema=vol.Schema(_AUTH_FIELDS), errors=errors)

    async def async_step_reauth_qr(self, user_input=None):
        errors = {}
        if user_input is not None:
            from custom_components.xiaomi_direct_pilot.xiaomi_direct.account import AccountError, XiaomiAccount
            try:
                client = self._reauth_client
                account = await XiaomiAccount.finish_qr(client, self._approval, self._region)
            except AccountError as error:
                code = getattr(error, "code", None)
                if code == "qr_pending":
                    return self.async_show_form(
                        step_id="reauth_qr", data_schema=vol.Schema({}), errors={"base": code},
                        description_placeholders={
                            "qr_image": str(self._approval.image), "qr_page": str(self._approval.page),
                        },
                    )
                elif code == "qr_expired":
                    await self._close_reauth_qr()
                    return self.async_abort(reason="qr_expired")
                else:
                    await self._close_reauth_qr()
                    self._approval = None
                    self._reauth_error = "cannot_sign_in"
                    return await self.async_step_reauth_confirm()
            except Exception as error:
                await self._close_reauth_qr()
                LOGGER.warning("Xiaomi QR approval failed: %s", type(error).__name__)
                self._approval = None
                self._reauth_error = "cannot_sign_in"
                return await self.async_step_reauth_confirm()

            try:
                await account.devices()
                result = await self._accept_reauth_account(account, inventory_loaded=True)
                await client.close()
                self._reauth_client = None
                return result
            except Exception as error:
                await self._close_reauth_qr()
                LOGGER.warning("Xiaomi reauthentication inventory failed: %s", type(error).__name__)
                self._approval = None
                self._reauth_error = "inventory_unavailable"
                return await self.async_step_reauth_confirm()
        return self.async_show_form(
            step_id="reauth_qr", data_schema=vol.Schema({}), errors=errors,
            description_placeholders={"qr_image": str(self._approval.image), "qr_page": str(self._approval.page)},
        )

    async def async_step_reauth_verify(self, user_input=None):
        errors = {}
        if user_input is not None:
            from custom_components.xiaomi_direct_pilot.xiaomi_direct.account import AccountError, create_client_session

            try:
                async with create_client_session() as client:
                    account = await self._reauth_verification(client, user_input["code"].strip())
                    await account.devices()
                return await self._accept_reauth_account(account, inventory_loaded=True)
            except AccountError:
                errors["base"] = "verification_failed"
        return self.async_show_form(
            step_id="reauth_verify", data_schema=vol.Schema({vol.Required("code"): _PASSWORD}), errors=errors,
        )

    async def _close_reauth_qr(self):
        client = getattr(self, "_reauth_client", None)
        if client is not None:
            await client.close()
            self._reauth_client = None

    async def _accept_reauth_account(self, account, *, inventory_loaded=False):
        entry = self._get_reauth_entry()
        if account.user_id != entry.options.get("direct_account_user_id"):
            return self.async_abort(reason="wrong_account")
        if not inventory_loaded:
            await account.devices()
        options = dict(entry.options)
        options["direct_account_pass_token"] = account.pass_token
        return self.async_update_reload_and_abort(entry, options=options)

    async def async_step_user(self, user_input=None):
        errors = {}
        if user_input is not None:
            url = user_input[CONF_URL].strip().rstrip("/")
            if not url.startswith(("http://", "https://")):
                errors["base"] = "invalid_url"
            else:
                try:
                    client = GatewayClient(async_get_clientsession(self.hass), url, user_input[CONF_TOKEN])
                    await client.cameras()
                except GatewayError as err:
                    LOGGER.warning("Could not connect to gateway: %s", err)
                    errors["base"] = "cannot_connect"
                else:
                    await self.async_set_unique_id(url.lower())
                    self._abort_if_unique_id_configured()
                    return self.async_create_entry(title=NAME, data={
                        CONF_URL: url, CONF_TOKEN: user_input[CONF_TOKEN],
                    })
        return self.async_show_form(
            step_id="user", errors=errors,
            data_schema=vol.Schema({
                vol.Required(CONF_URL,default="http://192.168.1.20:8099"): str,
                vol.Required(CONF_TOKEN): TextSelector(TextSelectorConfig(type=TextSelectorType.PASSWORD)),
            }),
        )


class XiaomiLocalGatewayOptionsFlow(OptionsFlowWithReload):
    """Select the active backend while retaining the gateway config for rollback."""

    async def async_step_init(self, user_input=None):
        if user_input is None:
            return self.async_show_form(
                step_id="init",
                data_schema=vol.Schema({
                    vol.Required("backend", default=self.config_entry.options.get("backend", "gateway")):
                        vol.In({"gateway": "Existing Go gateway", "direct": "Direct Xiaomi connection"}),
                }),
            )
        if user_input["backend"] == "gateway":
            return self.async_create_entry(
                title="", data={**self.config_entry.options, "backend": "gateway"},
            )
        return await self.async_step_direct_signin()

    async def async_step_direct_signin(self, user_input=None):
        errors = {}
        if getattr(self, "_direct_signin_error", None):
            errors["base"] = self._direct_signin_error
            self._direct_signin_error = None
        if user_input is not None:
            self._region = user_input["region"]
            if user_input["method"] == "qr":
                try:
                    from custom_components.xiaomi_direct_pilot.xiaomi_direct.account import XiaomiAccount, create_client_session

                    self._qr_client = create_client_session()
                    self._approval = await XiaomiAccount.start_qr(self._qr_client)
                    return await self.async_step_direct_qr()
                except Exception:
                    await self._close_direct_qr()
                    LOGGER.exception("Unable to start Xiaomi QR sign-in")
                    errors["base"] = "cannot_sign_in"
            elif not user_input.get("username", "").strip() or not user_input.get("password"):
                errors["base"] = "credentials_required"
            else:
                try:
                    from custom_components.xiaomi_direct_pilot.xiaomi_direct.account import XiaomiAccount, create_client_session

                    async with create_client_session() as client:
                        account = await XiaomiAccount.login(
                            client, user_input["username"].strip(), user_input["password"], self._region,
                        )
                        return await self._accept_direct_account(account)
                except Exception as error:
                    challenge = getattr(error, "code", None)
                    if challenge == "verification_required" and getattr(error, "continuation", None):
                        self._verify_account = error.continuation
                        return await self.async_step_direct_verify()
                    LOGGER.warning("Xiaomi direct sign-in failed: %s", type(error).__name__)
                    errors["base"] = "cannot_sign_in"
        schema = vol.Schema({
            vol.Required("region", default=getattr(self, "_region", "de")): vol.In(_REGIONS),
            **_AUTH_FIELDS,
        })
        return self.async_show_form(step_id="direct_signin", data_schema=schema, errors=errors)

    async def async_step_direct_qr(self, user_input=None):
        errors = {}
        if user_input is not None:
            try:
                from custom_components.xiaomi_direct_pilot.xiaomi_direct.account import XiaomiAccount, create_client_session

                client = self._qr_client
                account = await XiaomiAccount.finish_qr(client, self._approval, self._region)
            except Exception as error:
                code = getattr(error, "code", None)
                if code == "qr_expired":
                    await self._close_direct_qr()
                    return self.async_abort(reason="qr_expired")
                if code == "qr_pending":
                    errors["base"] = code
                else:
                    await self._close_direct_qr()
                    self._approval = None
                    self._direct_signin_error = "cannot_sign_in"
                    return await self.async_step_direct_signin()
            else:
                try:
                    result = await self._accept_direct_account(account)
                except Exception as error:
                    await self._close_direct_qr()
                    self._approval = None
                    self._direct_signin_error = "inventory_unavailable"
                    LOGGER.warning("Xiaomi direct camera inventory failed: %s", type(error).__name__)
                    return await self.async_step_direct_signin()
                await client.close()
                self._qr_client = None
                self._approval = None
                return result
        approval = self._approval
        if approval is None:
            return self.async_show_form(step_id="direct_qr", errors={"base": "cannot_sign_in"},
                                        data_schema=vol.Schema({}))
        return self.async_show_form(
            step_id="direct_qr", errors=errors, data_schema=vol.Schema({}),
            description_placeholders={"qr_image": str(approval.image), "qr_page": str(approval.page)},
        )

    async def async_step_direct_verify(self, user_input=None):
        errors = {}
        if user_input is not None:
            try:
                from custom_components.xiaomi_direct_pilot.xiaomi_direct.account import create_client_session

                async with create_client_session() as client:
                    account = await self._verify_account(client, user_input["code"].strip())
                    return await self._accept_direct_account(account)
            except Exception:
                errors["base"] = "verification_failed"
        return self.async_show_form(
            step_id="direct_verify", errors=errors,
            data_schema=vol.Schema({vol.Required("code"): _PASSWORD}),
        )

    async def _accept_direct_account(self, account):
        from custom_components.xiaomi_direct_pilot.inventory import account_cameras, resolve_cameras

        try:
            inventory = account_cameras(await account.devices())
        except Exception as error:
            LOGGER.warning("Unable to load Xiaomi camera inventory: %s", type(error).__name__)
            raise
        if not inventory:
            return self.async_abort(reason="no_cameras")
        self._direct_account = account
        self._direct_cameras = await resolve_cameras(self.hass, inventory)
        runtime_data = getattr(self.config_entry, "runtime_data", None)
        active = getattr(runtime_data, "data", {})
        self._gateway_camera_choices = {
            camera_id: item.get("name", camera_id) for camera_id, item in active.items()
        }
        return await self.async_step_direct_cameras()

    async def _close_direct_qr(self):
        client = getattr(self, "_qr_client", None)
        if client is not None:
            await client.close()
            self._qr_client = None

    async def async_step_direct_cameras(self, user_input=None):
        cameras = self._direct_cameras
        choices = {item["device_id"]: item["name"] for item in cameras}
        if user_input is not None:
            selected_ids = list(user_input["camera_ids"])
            id_map = dict(self.config_entry.options.get("direct_camera_id_map", {}))
            if getattr(self, "_gateway_camera_choices", None):
                for device_id in selected_ids:
                    target = user_input.get(f"map_{device_id}", "")
                    if target:
                        id_map[device_id] = target
                    else:
                        id_map.pop(device_id, None)
            mapped_ids = [id_map.get(device_id, device_id) for device_id in selected_ids]
            if len(set(mapped_ids)) != len(mapped_ids):
                return self.async_show_form(
                    step_id="direct_cameras", data_schema=self._direct_camera_schema(cameras),
                    errors={"base": "duplicate_camera_mapping"},
                )
            options = {
                "backend": "direct",
                "direct_region": self._region,
                "direct_account_user_id": self._direct_account.user_id,
                "direct_account_pass_token": self._direct_account.pass_token,
                "direct_cameras": cameras,
                "direct_camera_ids": selected_ids,
                "direct_camera_id_map": id_map,
            }
            options.update({key: list(user_input.get(key, ())) for key in ("c201_audio", "c400_low_resolution")})
            return self.async_create_entry(title="", data=options)

        return self.async_show_form(
            step_id="direct_cameras", data_schema=self._direct_camera_schema(cameras),
        )

    def _direct_camera_schema(self, cameras):
        choices = {item["device_id"]: item["name"] for item in cameras}
        fields = {vol.Required("camera_ids", default=list(choices)): cv.multi_select(choices)}
        gateway_choices = getattr(self, "_gateway_camera_choices", {})
        if gateway_choices:
            map_choices = {"": "Create a new camera entity"}
            map_choices.update(gateway_choices)
            saved = self.config_entry.options.get("direct_camera_id_map", {})
            for item in cameras:
                device_id = item["device_id"]
                default = saved.get(device_id, device_id if device_id in gateway_choices else "")
                fields[vol.Optional(f"map_{device_id}", default=default)] = vol.In(map_choices)
        if self._region == "de":
            c201s = {item["device_id"]: item["name"] for item in cameras if item["model"] == "xiaomi.camera.c201"}
            c400s = {item["device_id"]: item["name"] for item in cameras if item["model"] == "chuangmi.camera.039a04"}
            if c201s:
                fields[vol.Optional("c201_audio", default=[])] = cv.multi_select(c201s)
            if c400s:
                fields[vol.Optional("c400_low_resolution", default=[])] = cv.multi_select(c400s)
        return vol.Schema(fields)

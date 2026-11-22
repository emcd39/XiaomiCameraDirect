"""Authorize a Xiaomi account and discover its cameras."""

import logging

from homeassistant.config_entries import SOURCE_REAUTH, ConfigFlow, OptionsFlowWithReload
from homeassistant.core import callback
from homeassistant.helpers import config_validation as cv
from homeassistant.helpers.selector import TextSelector, TextSelectorConfig, TextSelectorType
import voluptuous as vol

from .const import (
    AW300_AUDIO, AW300_MODEL, C201_AUDIO, C201_MODEL, C400_AUDIO, C400_LOW_RESOLUTION,
    C400_MODEL, C701_AUDIO, C701_MODEL, DOMAIN, NAME,
)
from .inventory import account_cameras, excluded_camera_ids, resolve_cameras
from .xiaomi_direct.account import AccountChallenge, AccountError, XiaomiAccount, create_client_session


_REGIONS = ("de", "cn", "i2", "ru", "sg", "us")
_LOGGER = logging.getLogger(__name__)
_PASSWORD = TextSelector(TextSelectorConfig(type=TextSelectorType.PASSWORD))
_AUTH_FIELDS = {
    vol.Required("method", default="qr"): vol.In({"qr": "Xiaomi Home QR", "password": "Xiaomi account password"}),
    vol.Optional("username"): str,
    vol.Optional("password"): _PASSWORD,
}
_LOGIN_SCHEMA = vol.Schema({vol.Required("region", default="de"): vol.In(_REGIONS), **_AUTH_FIELDS})
_REAUTH_SCHEMA = vol.Schema(_AUTH_FIELDS)


class DirectCameraConfigFlow(ConfigFlow, domain=DOMAIN):
    VERSION = 3

    @staticmethod
    @callback
    def async_get_options_flow(config_entry):
        return DirectCameraOptionsFlow()

    async def _use_account(self, account):
        unique_id = f"{self._region}:{account.user_id}"
        if self.source == SOURCE_REAUTH:
            entry = self._get_reauth_entry()
            if account.user_id != entry.data["account_user_id"]:
                return self.async_abort(reason="wrong_account")
            await account.devices()
            return self.async_update_reload_and_abort(
                entry, data_updates={"account_pass_token": account.pass_token},
            )
        devices = account_cameras(await account.devices())
        if not devices:
            return self.async_abort(reason="no_cameras")
        cameras = await resolve_cameras(self.hass, devices)
        await self.async_set_unique_id(unique_id)
        self._abort_if_unique_id_configured()
        return self.async_create_entry(title=NAME, data={
            "region": self._region, "account_user_id": account.user_id,
            "account_pass_token": account.pass_token, "cameras": cameras,
        })

    async def async_step_user(self, user_input=None):
        return await self._sign_in("user", _LOGIN_SCHEMA, user_input)

    async def async_step_reauth(self, entry_data):
        self._region = entry_data["region"]
        return await self.async_step_reauth_confirm()

    async def async_step_reauth_confirm(self, user_input=None):
        return await self._sign_in("reauth_confirm", _REAUTH_SCHEMA, user_input)

    async def _sign_in(self, step, schema, user_input):
        errors = {}
        if user_input is not None:
            if self.source != SOURCE_REAUTH:
                self._region = user_input["region"]
            try:
                async with create_client_session() as client:
                    if user_input["method"] == "qr":
                        self._approval = await XiaomiAccount.start_qr(client)
                        return await self.async_step_qr()
                    if not user_input.get("username", "").strip() or not user_input.get("password"):
                        errors["base"] = "credentials_required"
                    else:
                        account = await XiaomiAccount.login(
                            client, user_input["username"].strip(), user_input["password"], self._region
                        )
                        return await self._use_account(account)
            except AccountChallenge as error:
                _LOGGER.info("Xiaomi direct sign-in requires %s", error.code)
                if error.code == "verification_required" and getattr(error, "continuation", None):
                    self._verify_xiaomi = error.continuation
                    return await self.async_step_verify()
                errors["base"] = error.code
            except AccountError as error:
                _LOGGER.warning("Xiaomi direct sign-in: %s", error)
                errors["base"] = "cannot_sign_in"
        return self.async_show_form(step_id=step, data_schema=schema, errors=errors)

    async def async_step_qr(self, user_input=None):
        errors = {}
        if user_input is not None:
            try:
                async with create_client_session() as client:
                    account = await XiaomiAccount.finish_qr(client, self._approval, self._region)
                    result = await self._use_account(account)
                    self._approval = None
                    return result
            except AccountChallenge as error:
                if error.code == "qr_expired":
                    return self.async_abort(reason="qr_expired")
                errors["base"] = error.code
            except AccountError as error:
                _LOGGER.warning("Xiaomi direct QR sign-in: %s", error)
                errors["base"] = "cannot_sign_in"
        return self.async_show_form(
            step_id="qr", errors=errors, data_schema=vol.Schema({}),
            description_placeholders={"qr_image": str(self._approval.image), "qr_page": str(self._approval.page)},
        )

    async def async_step_verify(self, user_input=None):
        errors = {}
        if user_input is not None:
            try:
                async with create_client_session() as client:
                    account = await self._verify_xiaomi(client, user_input["code"].strip())
                    result = await self._use_account(account)
                    self._verify_xiaomi = None
                    return result
            except AccountError as error:
                _LOGGER.warning("Xiaomi direct verification: %s", error)
                errors["base"] = "verification_failed"
        return self.async_show_form(
            step_id="verify", errors=errors,
            data_schema=vol.Schema({vol.Required("code"): _PASSWORD}),
        )


class DirectCameraOptionsFlow(OptionsFlowWithReload):
    async def async_step_init(self, user_input=None):
        cameras = self.config_entry.data["cameras"]
        options = self.config_entry.options
        choices = {item["device_id"]: item["name"] for item in cameras}
        excluded = excluded_camera_ids(cameras, options)
        if user_input is not None:
            selected = set(user_input["camera_ids"])
            excluded = (excluded - choices.keys()) | (choices.keys() - selected)
            return self.async_create_entry(data={**user_input, "excluded_camera_ids": sorted(excluded)})
        selected = [key for key in choices if key not in excluded]
        fields = {vol.Required("camera_ids", default=selected): cv.multi_select(choices)}
        if self.config_entry.data["region"] == "de":
            c201s = {item["device_id"]: item["name"] for item in cameras if item["model"] == C201_MODEL}
            if c201s:
                audio = [key for key in options.get(C201_AUDIO, ()) if key in c201s]
                fields[vol.Optional(C201_AUDIO, default=audio)] = cv.multi_select(c201s)
            c701s = {item["device_id"]: item["name"] for item in cameras if item["model"] == C701_MODEL}
            if c701s:
                audio = [key for key in options.get(C701_AUDIO, ()) if key in c701s]
                fields[vol.Optional(C701_AUDIO, default=audio)] = cv.multi_select(c701s)
            c400s = {item["device_id"]: item["name"] for item in cameras if item["model"] == C400_MODEL}
            if c400s:
                audio = [key for key in options.get(C400_AUDIO, ()) if key in c400s]
                fields[vol.Optional(C400_AUDIO, default=audio)] = cv.multi_select(c400s)
                reduced = [key for key in options.get(C400_LOW_RESOLUTION, ()) if key in c400s]
                fields[vol.Optional(C400_LOW_RESOLUTION, default=reduced)] = cv.multi_select(c400s)
            aw300s = {item["device_id"]: item["name"] for item in cameras if item["model"] == AW300_MODEL}
            if aw300s:
                audio = [key for key in options.get(AW300_AUDIO, ()) if key in aw300s]
                fields[vol.Optional(AW300_AUDIO, default=audio)] = cv.multi_select(aw300s)
        return self.async_show_form(
            step_id="init",
            data_schema=vol.Schema(fields),
        )

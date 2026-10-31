"""UI onboarding for a local Gateway URL and API token."""
from __future__ import annotations
import voluptuous as vol
from homeassistant.config_entries import ConfigFlow
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from homeassistant.helpers.selector import TextSelector, TextSelectorConfig, TextSelectorType
from .api import GatewayClient, GatewayError
from .const import CONF_TOKEN, CONF_URL, DOMAIN

class XiaomiLocalGatewayConfigFlow(ConfigFlow, domain=DOMAIN):
    VERSION = 1

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
                except GatewayError:
                    errors["base"] = "cannot_connect"
                else:
                    await self.async_set_unique_id(url.lower())
                    self._abort_if_unique_id_configured()
                    return self.async_create_entry(title="Xiaomi Local Gateway",data={
                        CONF_URL: url, CONF_TOKEN: user_input[CONF_TOKEN],
                    })
        return self.async_show_form(
            step_id="user", errors=errors,
            data_schema=vol.Schema({
                vol.Required(CONF_URL,default="http://192.168.1.20:8099"): str,
                vol.Required(CONF_TOKEN): TextSelector(TextSelectorConfig(type=TextSelectorType.PASSWORD)),
            }),
        )

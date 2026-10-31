"""Xiaomi Local Gateway Home Assistant integration."""
from __future__ import annotations
from homeassistant.const import Platform
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from .api import GatewayClient
from .coordinator import GatewayCoordinator
from .const import CONF_URL, CONF_TOKEN

PLATFORMS = [Platform.CAMERA, Platform.BUTTON]

async def async_setup_entry(hass, entry):
    client = GatewayClient(async_get_clientsession(hass),entry.data[CONF_URL],entry.data[CONF_TOKEN])
    coordinator = GatewayCoordinator(hass,entry,client)
    await coordinator.async_config_entry_first_refresh()
    entry.runtime_data = coordinator
    await hass.config_entries.async_forward_entry_setups(entry,PLATFORMS)
    return True

async def async_unload_entry(hass,entry):
    return await hass.config_entries.async_unload_platforms(entry,PLATFORMS)

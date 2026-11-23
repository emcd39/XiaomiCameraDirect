"""Xiaomi Local Gateway Home Assistant integration."""
from __future__ import annotations

from homeassistant.const import Platform
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from .api import GatewayClient
from .backend import GatewayBackendAdapter
from .coordinator import CameraSettingsSupport, GatewayCoordinator
from .const import CONF_URL, CONF_TOKEN

PLATFORMS = [Platform.CAMERA, Platform.BUTTON, Platform.SENSOR, Platform.BINARY_SENSOR]

async def async_setup_entry(hass, entry):
    if entry.options.get("backend", "gateway") == "direct":
        from .direct_backend import DirectBackendAdapter

        backend = await DirectBackendAdapter.create(hass, entry)
    else:
        client = GatewayClient(async_get_clientsession(hass), entry.data[CONF_URL], entry.data[CONF_TOKEN])
        backend = GatewayBackendAdapter(client)
    try:
        coordinator = GatewayCoordinator(hass, entry, backend)
        await coordinator.async_config_entry_first_refresh()
        entry.runtime_data = coordinator
        await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
        settings_support = CameraSettingsSupport(hass, entry, backend, coordinator)
        entry.async_on_unload(settings_support.stop)
        settings_support.start()
    except BaseException:
        await backend.close()
        raise
    return True

async def async_unload_entry(hass,entry):
    if not await hass.config_entries.async_unload_platforms(entry, PLATFORMS):
        return False
    await entry.runtime_data.client.close()
    return True

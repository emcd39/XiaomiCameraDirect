"""Read-only C201 diagnostics from the Xiaomi account."""

from datetime import timedelta
import logging

from homeassistant.helpers.entity import EntityCategory
from homeassistant.helpers.update_coordinator import CoordinatorEntity, DataUpdateCoordinator, UpdateFailed

from .inventory import camera_device_info
from .xiaomi_direct.account import AccountError


_LOGGER = logging.getLogger(__name__)


class C201SettingsCoordinator(DataUpdateCoordinator):
    def __init__(self, hass, entry, account, device_id):
        super().__init__(
            hass, _LOGGER, name="Xiaomi C201 cloud settings", config_entry=entry,
            update_interval=timedelta(minutes=5), always_update=False,
        )
        self._account = account
        self._device_id = device_id

    async def _async_update_data(self):
        try:
            return await self._account.read_c201_settings(self._device_id)
        except AccountError as error:
            raise UpdateFailed("Could not read C201 settings from Xiaomi") from error


class C201SettingEntity(CoordinatorEntity):
    """Poll only while at least one diagnostic entity is enabled."""

    _attr_has_entity_name = True
    _attr_entity_category = EntityCategory.DIAGNOSTIC
    _attr_entity_registry_enabled_default = False

    def __init__(self, coordinator, camera, key):
        super().__init__(coordinator)
        device_id = camera["device_id"]
        self._key = key
        self._attr_unique_id = f"xiaomi_direct_c201_{device_id}_{key}"
        self._attr_device_info = camera_device_info(camera["name"], device_id, camera["model"])

    async def async_added_to_hass(self):
        await super().async_added_to_hass()
        if self.coordinator.data is None:
            await self.coordinator.async_request_refresh()

    @property
    def available(self):
        return super().available and self._value is not None

    @property
    def _value(self):
        return (self.coordinator.data or {}).get(self._key)

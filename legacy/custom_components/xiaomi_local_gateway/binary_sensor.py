"""Read-only C201 configuration-enable indicators."""
from __future__ import annotations

from homeassistant.components.binary_sensor import BinarySensorEntity
from homeassistant.helpers.entity import EntityCategory
from homeassistant.helpers.update_coordinator import CoordinatorEntity

from .const import DOMAIN

SETTINGS = {
    "indicator_light": ("Indicator light enabled", "mdi:led-on"),
    "motion_detection": ("Motion detection enabled", "mdi:motion-sensor"),
    "human_detection": ("Human detection enabled", "mdi:human"),
    "move_detection": ("Movement detection enabled", "mdi:walk"),
    "pet_detection": ("Pet detection enabled", "mdi:paw"),
}


async def async_setup_entry(hass, entry, async_add_entities) -> None:
    cameras = entry.runtime_data
    seen: set[str] = set()

    def discover() -> None:
        settings = cameras.settings_coordinator
        if settings is None:
            return
        added = []
        for camera_id, camera in cameras.data.items():
            if camera.get("model") != "xiaomi.camera.c201" or camera_id in seen:
                continue
            seen.add(camera_id)
            added.extend(
                C201SettingBinarySensor(cameras, settings, camera_id, key)
                for key in SETTINGS
            )
        if added:
            async_add_entities(added)

    discover()
    entry.async_on_unload(cameras.async_add_listener(discover))


class C201SettingBinarySensor(CoordinatorEntity, BinarySensorEntity):
    """Report whether a C201 configuration option is enabled.

    These entities report MIoT setting values. They do not report live motion,
    person, or pet detection events.
    """

    _attr_has_entity_name = True
    _attr_entity_category = EntityCategory.DIAGNOSTIC

    def __init__(self, camera_coordinator, settings_coordinator, camera_id: str, key: str) -> None:
        super().__init__(settings_coordinator)
        self._camera_coordinator = camera_coordinator
        self._camera_id = camera_id
        self._key = key
        self._attr_name, self._attr_icon = SETTINGS[key]
        self._attr_unique_id = f"{camera_id}_{key}_enabled"

    @property
    def available(self) -> bool:
        if not self._camera_coordinator.last_update_success:
            return False
        if self._camera_id not in self._camera_coordinator.data:
            return False
        snapshot = self.coordinator.data.get(self._camera_id)
        setting = snapshot.get("properties", {}).get(self._key) if snapshot else None
        return (
            isinstance(setting, dict)
            and setting.get("code") == 0
            and type(setting.get("value")) is bool
        )

    @property
    def is_on(self) -> bool | None:
        snapshot = self.coordinator.data.get(self._camera_id)
        setting = snapshot.get("properties", {}).get(self._key) if snapshot else None
        if not isinstance(setting, dict) or setting.get("code") != 0:
            return None
        value = setting.get("value")
        return value if type(value) is bool else None

    @property
    def extra_state_attributes(self) -> dict[str, object]:
        snapshot = self.coordinator.data.get(self._camera_id)
        if snapshot is None:
            return {}
        setting = snapshot.get("properties", {}).get(self._key)
        if not isinstance(setting, dict):
            return {"observed_at": snapshot.get("observed_at")}
        return {
            "raw_miot_value": setting.get("value"),
            "miot_code": setting.get("code"),
            "observed_at": snapshot.get("observed_at"),
        }

    @property
    def device_info(self) -> dict[str, object]:
        camera = self._camera_coordinator.data.get(self._camera_id, {})
        return {
            "identifiers": {(DOMAIN, self._camera_id)},
            "name": camera.get("name", self._camera_id),
            "manufacturer": "Xiaomi",
            "model": camera.get("model", "Unknown"),
        }

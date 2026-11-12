"""Read-only C201 night-vision mode sensor."""
from __future__ import annotations

from homeassistant.helpers.update_coordinator import CoordinatorEntity
from homeassistant.components.sensor import SensorEntity
from homeassistant.helpers.entity import EntityCategory
from homeassistant.core import callback

from .const import DOMAIN

NIGHT_SHOT_MODES = {0: "Off", 1: "On", 2: "Auto"}


async def async_setup_entry(hass, entry, async_add_entities) -> None:
    cameras = entry.runtime_data
    seen: set[str] = set()

    @callback
    def discover() -> None:
        settings = cameras.settings_coordinator
        if settings is None:
            return
        added = []
        for camera_id, camera in cameras.data.items():
            if camera.get("model") != "xiaomi.camera.c201" or camera_id in seen:
                continue
            seen.add(camera_id)
            added.append(NightShotSensor(cameras, settings, camera_id))
        if added:
            async_add_entities(added)

    discover()
    entry.async_on_unload(cameras.async_add_listener(discover))


class NightShotSensor(CoordinatorEntity, SensorEntity):
    """Expose Night Shot as a diagnostic sensor; this entity cannot write it."""

    _attr_has_entity_name = True
    _attr_entity_category = EntityCategory.DIAGNOSTIC
    _attr_icon = "mdi:weather-night"
    _attr_name = "Night vision mode"

    def __init__(self, camera_coordinator, settings_coordinator, camera_id: str) -> None:
        super().__init__(settings_coordinator)
        self._camera_coordinator = camera_coordinator
        self._camera_id = camera_id
        self._attr_unique_id = f"{camera_id}_night_shot_mode"

    @property
    def available(self) -> bool:
        if not self._camera_coordinator.last_update_success:
            return False
        if self._camera_id not in self._camera_coordinator.data:
            return False
        snapshot = self.coordinator.data.get(self._camera_id)
        if snapshot is None:
            return False
        setting = snapshot.get("properties", {}).get("night_shot")
        return (
            isinstance(setting, dict)
            and setting.get("code") == 0
            and type(setting.get("value")) is int
        )

    @property
    def native_value(self) -> str | None:
        snapshot = self.coordinator.data.get(self._camera_id)
        if snapshot is None:
            return None
        setting = snapshot.get("properties", {}).get("night_shot")
        if not isinstance(setting, dict) or setting.get("code") != 0:
            return None
        value = setting.get("value")
        if type(value) is not int:
            return None
        return NIGHT_SHOT_MODES.get(value, f"Unknown ({value})")

    @property
    def extra_state_attributes(self) -> dict[str, object]:
        snapshot = self.coordinator.data.get(self._camera_id)
        if snapshot is None:
            return {}
        setting = snapshot.get("properties", {}).get("night_shot")
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

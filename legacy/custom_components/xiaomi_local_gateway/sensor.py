"""Read-only C201 night-vision mode sensor."""
from __future__ import annotations

from homeassistant.helpers.update_coordinator import CoordinatorEntity
from homeassistant.components.sensor import SensorEntity
from homeassistant.helpers.entity import EntityCategory
from homeassistant.core import callback

from .const import DOMAIN

NIGHT_SHOT_MODES = {0: "Off", 1: "On", 2: "Auto"}
STORAGE_PROPERTIES = {
    "card_status": ("SD card status (raw)", "mdi:sd"),
    "total": ("SD total capacity (raw)", "mdi:database"),
    "free": ("SD free capacity (raw)", "mdi:database-outline"),
    "used": ("SD used capacity (raw)", "mdi:database-check"),
}


async def async_setup_entry(hass, entry, async_add_entities) -> None:
    cameras = entry.runtime_data
    seen: set[str] = set()
    seen_storage: set[tuple[str, str]] = set()

    @callback
    def discover() -> None:
        settings = cameras.settings_coordinator
        storage = cameras.storage_coordinator
        added = []
        for camera_id, camera in cameras.data.items():
            if camera.get("model") != "xiaomi.camera.c201":
                continue
            if settings is not None and camera_id not in seen:
                seen.add(camera_id)
                added.append(NightShotSensor(cameras, settings, camera_id))
            if storage is not None:
                for key in STORAGE_PROPERTIES:
                    storage_key = (camera_id, key)
                    if storage_key not in seen_storage:
                        seen_storage.add(storage_key)
                        added.append(C201StorageSensor(cameras, storage, camera_id, key))
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


class C201StorageSensor(CoordinatorEntity, SensorEntity):
    """Expose a raw MIoT storage value without interpreting its unit/status."""

    _attr_has_entity_name = True
    _attr_entity_category = EntityCategory.DIAGNOSTIC
    _attr_native_unit_of_measurement = None

    def __init__(self, camera_coordinator, storage_coordinator, camera_id: str, key: str) -> None:
        super().__init__(storage_coordinator)
        self._camera_coordinator = camera_coordinator
        self._camera_id = camera_id
        self._key = key
        self._attr_name, self._attr_icon = STORAGE_PROPERTIES[key]
        self._attr_unique_id = f"{camera_id}_sd_{key}_raw"

    async def async_added_to_hass(self) -> None:
        await super().async_added_to_hass()
        self.async_on_remove(
            self._camera_coordinator.async_add_listener(self._handle_camera_update)
        )
        # On the first SD poll, the camera may return while that initial
        # request is in flight and before this coordinator is published to
        # the camera coordinator. Catch its missing snapshot when the entity
        # is subsequently registered.
        if self._camera_id in self._camera_coordinator.data:
            self.coordinator.request_missing_camera(self._camera_id)

    @callback
    def _handle_camera_update(self) -> None:
        if self._camera_id in self._camera_coordinator.data:
            self.coordinator.request_missing_camera(self._camera_id)
        self.async_write_ha_state()

    @property
    def available(self) -> bool:
        if not self._camera_coordinator.last_update_success or not self.coordinator.last_update_success:
            return False
        if self._camera_id not in self._camera_coordinator.data:
            return False
        snapshot = self.coordinator.data.get(self._camera_id)
        property_value = snapshot.get("properties", {}).get(self._key) if snapshot else None
        return (
            isinstance(property_value, dict)
            and property_value.get("code") == 0
            and type(property_value.get("value")) is int
        )

    @property
    def native_value(self) -> int | None:
        snapshot = self.coordinator.data.get(self._camera_id)
        property_value = snapshot.get("properties", {}).get(self._key) if snapshot else None
        if (
            not isinstance(property_value, dict)
            or property_value.get("code") != 0
            or type(property_value.get("value")) is not int
        ):
            return None
        return property_value["value"]

    @property
    def extra_state_attributes(self) -> dict[str, object]:
        snapshot = self.coordinator.data.get(self._camera_id)
        if snapshot is None:
            return {"unit": None}
        property_value = snapshot.get("properties", {}).get(self._key)
        if not isinstance(property_value, dict):
            return {"observed_at": snapshot.get("observed_at"), "unit": None}
        return {
            "raw_miot_value": property_value.get("value"),
            "miot_code": property_value.get("code"),
            "observed_at": snapshot.get("observed_at"),
            "unit": None,
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

"""C201 night vision and SD card diagnostics."""

from homeassistant.components.sensor import SensorEntity

from .settings import C201SettingEntity


_NIGHT_SHOT_MODES = {0: "Off", 1: "On", 2: "Auto"}
_SD_CARD_ICONS = {
    "sd_card_status": "mdi:sd",
    "sd_card_total": "mdi:database",
    "sd_card_free": "mdi:database-outline",
    "sd_card_used": "mdi:database-check",
}


async def async_setup_entry(hass, entry, async_add_entities):
    runtime = entry.runtime_data
    entities = []
    for camera in runtime.cameras:
        coordinator = runtime.settings.get(camera["device_id"])
        if coordinator is None:
            continue
        entities.append(NightVisionSensor(coordinator, camera, "night_shot"))
        entities.extend(SdCardSensor(coordinator, camera, key) for key in _SD_CARD_ICONS)
    async_add_entities(entities)


class NightVisionSensor(C201SettingEntity, SensorEntity):
    _attr_translation_key = "night_vision_mode"
    _attr_icon = "mdi:weather-night"

    @property
    def native_value(self):
        value = self._value
        if value is None:
            return None
        return _NIGHT_SHOT_MODES.get(value, str(value))


class SdCardSensor(C201SettingEntity, SensorEntity):
    """Expose the unscaled MIoT value until device units are verified."""

    def __init__(self, coordinator, camera, key):
        super().__init__(coordinator, camera, key)
        self._attr_translation_key = key
        self._attr_icon = _SD_CARD_ICONS[key]

    @property
    def native_value(self):
        return self._value

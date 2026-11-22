"""C201 indicator light and detection configuration diagnostics."""

from homeassistant.components.binary_sensor import BinarySensorEntity

from .settings import C201SettingEntity


_ICONS = {
    "indicator_light": "mdi:led-on",
    "motion_detection": "mdi:motion-sensor",
    "human_detection": "mdi:human",
    "pet_detection": "mdi:paw",
}


async def async_setup_entry(hass, entry, async_add_entities):
    runtime = entry.runtime_data
    async_add_entities(
        C201SettingBinarySensor(runtime.settings[camera["device_id"]], camera, key)
        for camera in runtime.cameras if camera["device_id"] in runtime.settings
        for key in _ICONS
    )


class C201SettingBinarySensor(C201SettingEntity, BinarySensorEntity):
    """Report a setting from the camera configuration."""

    def __init__(self, coordinator, camera, key):
        super().__init__(coordinator, camera, key)
        self._attr_translation_key = key
        self._attr_icon = _ICONS[key]

    @property
    def is_on(self):
        return self._value

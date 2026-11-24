"""Step controls for the cameras with a mapped MISS PTZ profile."""

import asyncio

from homeassistant.components.button import ButtonEntity
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers.dispatcher import async_dispatcher_connect, async_dispatcher_send

from .const import C701_MODEL, PTZ2K_MODEL, PTZ3_MODEL, PTZ_OPERATIONS
from .inventory import camera_device_info
from .xiaomi_direct.cs2_tcp import CS2Error


PTZ_MODELS = frozenset({
    "xiaomi.camera.c201", "chuangmi.camera.039a04", "chuangmi.camera.026c02", C701_MODEL,
    PTZ2K_MODEL, PTZ3_MODEL,
})


class PTZControl:
    def __init__(self, hass, device_id):
        self.hass = hass
        self.signal = f"xiaomi_direct_ptz_{device_id}"
        self.video = None
        self._lock = asyncio.Lock()

    def set_video(self, video):
        self.video = video
        async_dispatcher_send(self.hass, self.signal)

    async def press(self, direction):
        video = self.video
        if video is None:
            raise HomeAssistantError("Open the camera's live stream first")
        try:
            async with asyncio.timeout(2):
                async with self._lock:
                    if self.video is not video:
                        raise HomeAssistantError("Camera stream restarted; press the direction again")
                    await video.step(PTZ_OPERATIONS[direction])
        except (CS2Error, OSError, TimeoutError):
            raise HomeAssistantError("Camera did not confirm that the PTZ command was sent") from None


class DirectPTZButton(ButtonEntity):
    def __init__(self, item, direction, control):
        self._attr_name = f"{item['name']} PTZ {direction.capitalize()}"
        self._attr_unique_id = f"xiaomi_direct_ptz_{item['device_id']}_{direction}"
        self._attr_device_info = camera_device_info(item["name"], item["device_id"], item["model"])
        self._attr_icon = f"mdi:arrow-{direction}-bold"
        self._direction = direction
        self._control = control

    @property
    def available(self):
        return self._control.video is not None

    async def async_added_to_hass(self):
        await super().async_added_to_hass()
        self.async_on_remove(async_dispatcher_connect(
            self.hass, self._control.signal, self.async_write_ha_state,
        ))

    async def async_press(self):
        await self._control.press(self._direction)


async def async_setup_entry(hass, entry, async_add_entities):
    runtime = entry.runtime_data
    async_add_entities([
        DirectPTZButton(item, direction, runtime.ptz[item["device_id"]])
        for item in runtime.cameras if item["device_id"] in runtime.ptz
        for direction in PTZ_OPERATIONS
    ])

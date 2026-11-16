"""Discrete PTZ buttons per camera."""
from __future__ import annotations
from homeassistant.components.button import ButtonEntity
from homeassistant.core import callback
from homeassistant.exceptions import HomeAssistantError
from .api import GatewayError
from .entity import GatewayEntity

DIRECTIONS = ("up", "down", "left", "right")

async def async_setup_entry(hass, entry, async_add_entities):
    coordinator=entry.runtime_data
    seen=set()
    @callback
    def discover():
        added=[]
        for did, cam in coordinator.data.items():
            if not cam.get("ptz"):
                continue
            for direction in DIRECTIONS:
                key=(did,direction)
                if key not in seen:
                    seen.add(key)
                    added.append(PTZButton(coordinator,did,direction))
        if added:
            async_add_entities(added)
    discover()
    entry.async_on_unload(coordinator.async_add_listener(discover))

class PTZButton(GatewayEntity, ButtonEntity):
    def __init__(self,coordinator,did,direction):
        super().__init__(coordinator,did)
        self.direction=direction
        self._attr_unique_id=f"{did}_ptz_{direction}"
        self._attr_name=f"PTZ {direction.capitalize()}"
        self._attr_icon={"up":"mdi:arrow-up-bold","down":"mdi:arrow-down-bold",
                         "left":"mdi:arrow-left-bold","right":"mdi:arrow-right-bold"}[direction]

    async def async_press(self):
        camera = self.coordinator.data.get(self.did, {})
        if not camera.get("ptz"):
            raise HomeAssistantError("PTZ is unavailable for this camera")
        managed = camera.get("managed_steps", False)
        client = self.coordinator.client
        try:
            if managed and camera.get("on_demand_stream"):
                await client.camera_stream(self.did)
            await client.ptz_step(self.did, self.direction, managed=managed)
        except GatewayError as err:
            raise HomeAssistantError(str(err)) from err

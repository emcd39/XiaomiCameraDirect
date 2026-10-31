"""Home Assistant camera entity providing RTSP directly."""
from __future__ import annotations
from homeassistant.components.camera import Camera, CameraEntityFeature
from homeassistant.core import callback
from .entity import GatewayEntity

async def async_setup_entry(hass, entry, async_add_entities):
    coordinator = entry.runtime_data
    seen = set()
    @callback
    def discover():
        added = []
        for did in coordinator.data:
            if did not in seen:
                seen.add(did)
                added.append(GatewayCamera(coordinator, did))
        if added:
            async_add_entities(added)
    discover()
    entry.async_on_unload(coordinator.async_add_listener(discover))

class GatewayCamera(GatewayEntity, Camera):
    _attr_supported_features = CameraEntityFeature.STREAM
    _attr_name = None

    def __init__(self,coordinator,did):
        GatewayEntity.__init__(self,coordinator,did)
        Camera.__init__(self)
        self._attr_unique_id = f"{did}_camera"

    async def stream_source(self) -> str | None:
        camera = self.coordinator.data.get(self.did)
        return camera.get("rtsp_url") if camera else None

    async def async_camera_image(self, width=None, height=None) -> bytes | None:
        # Avoid inventing stills. HA's stream component uses RTSP for live view.
        return None

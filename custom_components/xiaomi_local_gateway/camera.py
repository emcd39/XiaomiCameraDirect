"""Home Assistant camera entity providing RTSP directly."""
from __future__ import annotations
from homeassistant.components import ffmpeg
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

    @property
    def supported_features(self) -> CameraEntityFeature:
        camera = self.coordinator.data.get(self.did, {})
        return CameraEntityFeature.STREAM if camera.get("rtsp_url") else CameraEntityFeature(0)

    async def stream_source(self) -> str | None:
        camera = self.coordinator.data.get(self.did)
        return camera.get("rtsp_url") if camera else None

    @property
    def extra_state_attributes(self) -> dict[str, object]:
        camera = self.coordinator.data.get(self.did, {})
        model_id = camera.get("model")
        attributes: dict[str, object] = {}
        if isinstance(model_id, str) and model_id:
            attributes["model_id"] = model_id
        support = camera.get("support")
        if isinstance(support, dict):
            attributes["support"] = support
        return attributes

    async def async_camera_image(self, width=None, height=None) -> bytes | None:
        camera = self.coordinator.data.get(self.did, {})
        source = camera.get("rtsp_url")
        if not source:
            return None
        # Decode the RTSP source inside Home Assistant. The gateway's host may
        # not have FFmpeg installed (for example, a Windows gateway), while HA
        # already ships the decoder used for its live camera stream.
        return await ffmpeg.async_get_image(self.hass, source, width=width, height=height)

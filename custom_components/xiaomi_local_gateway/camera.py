"""Home Assistant camera entity providing RTSP directly."""
from __future__ import annotations
from contextvars import ContextVar
from homeassistant.components import ffmpeg
from homeassistant.components.camera import Camera, CameraEntityFeature
from homeassistant.core import callback
from .api import GatewayError
from .entity import GatewayEntity

_provider_probe_camera = ContextVar("xlg_provider_probe_camera", default=None)

_SUPPORT_REASON_TEXT = {
    "auth_required": "Camera authorization is required.",
    "wrong_region_or_empty_account": "The Xiaomi account region or inventory is unavailable.",
    "unsupported_model": "This camera model is not supported by the current provider.",
    "unsupported_transport": "This camera transport is not supported.",
    "unverified_firmware": "Current camera firmware has not been verified for this feature.",
    "channel_unverified": "The required camera channel has not been verified.",
    "timeout": "The camera did not respond before the timeout.",
    "stale": "This diagnostic result is stale.",
    "lan_unreachable": "The camera is not reachable on the local network.",
    "stream_timeout": "The camera stream timed out.",
    "codec_unsupported": "The camera video codec is not supported.",
    "backend_unavailable": "The gateway camera provider is unavailable.",
    "storage_error": "Camera storage could not be read.",
    "api_incompatible": "The gateway API version is not compatible.",
}

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
        return CameraEntityFeature.STREAM if camera.get("rtsp_url") or camera.get("on_demand_stream") else CameraEntityFeature(0)

    async def async_refresh_providers(self, *, write_state: bool = True) -> None:
        # HA calls stream_source during WebRTC discovery when an entity is
        # added. This probe must not open an on-demand source.
        probe = _provider_probe_camera.set(self)
        try:
            await super().async_refresh_providers(write_state=write_state)
        finally:
            _provider_probe_camera.reset(probe)

    async def stream_source(self) -> str | None:
        camera = self.coordinator.data.get(self.did)
        if not camera:
            return None
        if not camera.get("available", True):
            return None
        if camera.get("on_demand_stream"):
            if _provider_probe_camera.get() is self:
                return None
            try:
                return await self.coordinator.client.camera_stream(self.did)
            except GatewayError:
                return None
        return camera.get("rtsp_url")

    @property
    def extra_state_attributes(self) -> dict[str, object]:
        camera = self.coordinator.data.get(self.did, {})
        model_id = camera.get("model")
        attributes: dict[str, object] = {}
        if isinstance(model_id, str) and model_id:
            attributes["model_id"] = model_id
        for key in ("source_codec", "source_resolution"):
            if key in camera:
                attributes[key] = camera[key]
        support = camera.get("support")
        if isinstance(support, dict):
            displayed_support = {
                "schema_version": support.get("schema_version"),
                "features": {},
            }
            for name, feature in support.get("features", {}).items():
                if not isinstance(feature, dict):
                    continue
                detail = dict(feature)
                reason = detail.get("reason")
                if isinstance(reason, str) and reason in _SUPPORT_REASON_TEXT:
                    detail["reason_text"] = _SUPPORT_REASON_TEXT[reason]
                displayed_support["features"][name] = detail
            if displayed_support["features"]:
                attributes["support"] = displayed_support
        return attributes

    @property
    def capability_attributes(self) -> dict[str, object] | None:
        status = self.coordinator.data.get(self.did, {}).get("direct_status")
        return {"direct_status": status} if isinstance(status, str) else None

    async def async_camera_image(self, width=None, height=None) -> bytes | None:
        source = await self.stream_source()
        if not source:
            return None
        # Decode the RTSP source inside Home Assistant. The gateway's host may
        # not have FFmpeg installed (for example, a Windows gateway), while HA
        # already ships the decoder used for its live camera stream.
        return await ffmpeg.async_get_image(self.hass, source, width=width, height=height)

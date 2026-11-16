"""Async gateway client, using Home Assistant's shared session."""
from __future__ import annotations
import json
import re
from aiohttp import ClientResponseError, ClientSession, ClientError, ClientTimeout
from urllib.parse import quote, urlsplit


class GatewayError(Exception):
    """Error communicating with the gateway."""


class GatewayUnauthorized(GatewayError):
    """Authentication rejected."""


class GatewayEndpointUnavailable(GatewayError):
    """The managed API is absent, so a legacy read may be used."""


class GatewayPTZSessionUnavailable(GatewayError):
    """The gateway explicitly rejected PTZ before sending the command."""


class GatewayClient:
    def __init__(self, session: ClientSession, base_url: str, token: str) -> None:
        self._session = session
        self._url = base_url.rstrip("/")
        self._token = token
        self._include_inventory_streams = False

    async def _call(self, method: str, path: str, payload: dict | None = None, *, timeout: int = 15):
        try:
            async with self._session.request(
                method, self._url + path,
                headers={"Authorization": f"Bearer {self._token}"},
                json=payload,
                timeout=ClientTimeout(total=timeout),
            ) as response:
                if response.status == 401:
                    raise GatewayUnauthorized("Gateway rejected API token")
                if response.status == 404:
                    raise GatewayEndpointUnavailable("Managed camera API is unavailable")
                if response.status == 503:
                    try:
                        error = await response.json()
                    except (json.JSONDecodeError, ValueError):
                        error = None
                    if isinstance(error, dict) and error.get("error") == "ptz_session_unavailable":
                        raise GatewayPTZSessionUnavailable("Camera control session is not active")
                    if isinstance(error, dict) and error.get("error") == "backend_unavailable":
                        raise GatewayEndpointUnavailable("Managed camera API is unavailable")
                response.raise_for_status()
                return await response.json()
        except (GatewayUnauthorized, GatewayEndpointUnavailable):
            raise
        except (ClientResponseError, ClientError, TimeoutError, json.JSONDecodeError, ValueError) as err:
            # Don't log a full upstream URL: the config may eventually contain secrets.
            raise GatewayError(f"Gateway request failed ({type(err).__name__})") from err

    async def cameras(self) -> list[dict]:
        path = "/v2/cameras"
        if not self._include_inventory_streams:
            path += "?include_streams=false"
        try:
            result = await self._call("GET", path)
            cameras = self._parse_cameras(result)
            on_demand = result.get("on_demand_streams") is True
            if not self._include_inventory_streams and not on_demand and any(
                raw["capabilities"].get("video") is True and not camera["rtsp_url"]
                for raw, camera in zip(result["cameras"], cameras, strict=True)
            ):
                # Older gateways with the inventory filter have no separate
                # readiness route. Keep their previous source lookup behavior.
                result = await self._call("GET", "/v2/cameras")
                cameras = self._parse_cameras(result)
                self._include_inventory_streams = result.get("on_demand_streams") is not True
            elif on_demand:
                self._include_inventory_streams = False
            return cameras
        except GatewayEndpointUnavailable:
            return await self._legacy_cameras()

    @staticmethod
    def _parse_cameras(result) -> list[dict]:
        cameras = result.get("cameras") if isinstance(result, dict) else None
        if not isinstance(cameras, list):
            raise GatewayError("Invalid camera inventory response")
        normalized = []
        for camera in cameras:
            if not isinstance(camera, dict):
                raise GatewayError("Invalid camera inventory response")
            camera_id = camera.get("id")
            name = camera.get("name")
            capabilities = camera.get("capabilities")
            streams = camera.get("streams", [])
            if (
                not isinstance(camera_id, str) or not camera_id
                or not isinstance(name, str) or not name
                or not isinstance(capabilities, dict)
                or not isinstance(capabilities.get("ptz"), bool)
                or not isinstance(streams, list)
            ):
                raise GatewayError("Invalid camera inventory response")
            source = _rtsp_stream_url(streams)
            normalized_camera = {
                "id": camera_id,
                "name": name,
                "model": camera.get("model", "Unknown"),
                "rtsp_url": source,
                "ptz": capabilities["ptz"],
            }
            if result.get("on_demand_streams") is True and capabilities.get("video") is True:
                normalized_camera["on_demand_stream"] = True
            if result.get("managed_steps") is True and capabilities["ptz"]:
                normalized_camera["managed_steps"] = True
            video = camera.get("video")
            if isinstance(video, dict) and video.get("state") == "streaming":
                source_format = video.get("source_format")
                if isinstance(source_format, dict) and source_format.get("codec") in ("H264", "H265"):
                    normalized_camera["source_codec"] = source_format["codec"]
                    width, height = source_format.get("width"), source_format.get("height")
                    if type(width) is int and type(height) is int and width > 0 and height > 0:
                        normalized_camera["source_resolution"] = f"{width}×{height}"
            support = _parse_camera_support(camera.get("support"))
            if support is not None:
                normalized_camera["support"] = support
            normalized.append(normalized_camera)
        if len({camera["id"] for camera in normalized}) != len(normalized):
            raise GatewayError("Invalid camera inventory response")
        return normalized

    async def camera_settings(self, camera_id: str) -> dict:
        result = await self._call(
            "GET", f"/v2/cameras/{quote(camera_id, safe='')}/settings"
        )
        properties = result.get("properties") if isinstance(result, dict) else None
        observed_at = result.get("observed_at") if isinstance(result, dict) else None
        if not isinstance(observed_at, str) or not isinstance(properties, dict):
            raise GatewayError("Invalid camera settings response")
        property_types = {
            "night_shot": int,
            "indicator_light": bool,
            "motion_detection": bool,
            "human_detection": bool,
            "move_detection": bool,
            "pet_detection": bool,
        }
        normalized = {}
        for name, setting in properties.items():
            expected_type = property_types.get(name)
            value = setting.get("value") if isinstance(setting, dict) else None
            if (
                expected_type is None
                or not isinstance(setting, dict)
                or type(setting.get("code")) is not int
                or (value is not None and type(value) is not expected_type)
            ):
                raise GatewayError("Invalid camera settings response")
            normalized[name] = {"code": setting["code"], "value": value}
        if "night_shot" not in normalized:
            raise GatewayError("Invalid camera settings response")
        return {
            "observed_at": observed_at,
            "properties": normalized,
        }

    async def camera_storage(self, camera_id: str) -> dict:
        result = await self._call(
            "GET", f"/v2/cameras/{quote(camera_id, safe='')}/storage"
        )
        properties = result.get("properties") if isinstance(result, dict) else None
        observed_at = result.get("observed_at") if isinstance(result, dict) else None
        if not isinstance(observed_at, str) or not isinstance(properties, dict):
            raise GatewayError("Invalid camera storage response")
        normalized = {}
        property_maximums = {
            "card_status": 100,
            "total": 1 << 31,
            "free": 1 << 31,
            "used": 1 << 31,
        }
        for name, item in properties.items():
            value = item.get("value") if isinstance(item, dict) else None
            if (
                name not in property_maximums
                or not isinstance(item, dict)
                or type(item.get("code")) is not int
                or (
                    value is not None
                    and (type(value) is not int or value < 0 or value > property_maximums[name])
                )
                or "unit" not in item
                or item["unit"] is not None
            ):
                raise GatewayError("Invalid camera storage response")
            normalized[name] = {
                "code": item["code"],
                "value": value,
                "unit": None,
            }
        return {"observed_at": observed_at, "properties": normalized}

    async def camera_read_capabilities(self) -> set[str]:
        """Return the optional read-only feature flags advertised by a gateway."""
        try:
            result = await self._call("GET", "/v2/info")
        except GatewayEndpointUnavailable:
            return set()
        if not isinstance(result, dict):
            return set()
        features = result.get("features")
        if not isinstance(features, list) or any(not isinstance(feature, str) for feature in features):
            return set()
        return {feature for feature in features if feature in {"camera_settings_read", "sd_capacity"}}

    async def supports_camera_settings(self) -> bool:
        """Check the managed API feature flag before scheduling settings polls."""
        return "camera_settings_read" in await self.camera_read_capabilities()

    async def supports_camera_storage(self) -> bool:
        """Check the feature flag before polling the storage endpoint."""
        return "sd_capacity" in await self.camera_read_capabilities()

    async def _legacy_cameras(self) -> list[dict]:
        result = await self._call("GET", "/v1/cameras")
        cameras = result.get("cameras") if isinstance(result, dict) else None
        if not isinstance(cameras, list) or any(
            not isinstance(camera, dict)
            or not isinstance(camera.get("id"), str)
            or not camera["id"]
            or not isinstance(camera.get("rtsp_url"), str)
            or not _valid_rtsp_url(camera["rtsp_url"])
            or not isinstance(camera.get("ptz"), bool)
            for camera in cameras
        ) or len({camera["id"] for camera in cameras}) != len(cameras):
            raise GatewayError("Invalid camera inventory response")
        return cameras

    async def camera_stream(self, camera_id: str) -> str:
        result = await self._call("GET", f"/v2/cameras/{quote(camera_id, safe='')}/stream", timeout=18)
        streams = result.get("streams") if isinstance(result, dict) else None
        source = _rtsp_stream_url(streams)
        if source is None:
            raise GatewayError("Gateway did not return a ready RTSP source")
        return source

    async def ptz_step(self, camera_id: str, direction: str, *, managed: bool = False) -> None:
        version = "v2" if managed else "v1"
        result = await self._call(
            "POST", f"/{version}/cameras/{quote(camera_id, safe='')}/ptz/step", {"direction": direction}
        )
        if managed and (not isinstance(result, dict) or result.get("write_status") != "sent"):
            raise GatewayError("Gateway did not confirm the PTZ step")

    async def ptz_stop(self, camera_id: str) -> None:
        await self._call("POST", f"/v1/cameras/{camera_id}/ptz/stop", {})

    async def snapshot(self, camera_id: str) -> bytes:
        try:
            async with self._session.get(
                self._url + f"/v2/cameras/{quote(camera_id, safe='')}/snapshot",
                headers={"Authorization": f"Bearer {self._token}"},
                timeout=ClientTimeout(total=15),
            ) as response:
                if response.status == 401:
                    raise GatewayUnauthorized("Gateway rejected API token")
                response.raise_for_status()
                if response.headers.get("Content-Type", "").split(";", 1)[0].lower() != "image/jpeg":
                    raise GatewayError("Gateway returned an invalid camera snapshot")
                data = await response.content.read(8 * 1024 * 1024 + 1)
                if len(data) > 8 * 1024 * 1024 or not data.startswith(b"\xff\xd8") or not data.endswith(b"\xff\xd9"):
                    raise GatewayError("Gateway returned an invalid camera snapshot")
                return data
        except (GatewayUnauthorized, GatewayError):
            raise
        except (ClientResponseError, ClientError, TimeoutError, ValueError) as err:
            raise GatewayError(f"Gateway snapshot request failed ({type(err).__name__})") from err


def _rtsp_stream_url(streams) -> str | None:
    if not isinstance(streams, list):
        raise GatewayError("Invalid camera stream response")
    for stream in streams:
        if not isinstance(stream, dict):
            raise GatewayError("Invalid camera stream response")
        if stream.get("protocol") == "rtsp":
            source = stream.get("url")
            if not isinstance(source, str) or not _valid_rtsp_url(source):
                raise GatewayError("Invalid camera stream response")
            return source
    return None


def _valid_rtsp_url(value: str) -> bool:
    try:
        parsed = urlsplit(value)
        return parsed.scheme == "rtsp" and bool(parsed.hostname) and parsed.username is None and parsed.password is None and not parsed.query and not parsed.fragment
    except ValueError:
        return False


_SUPPORT_FEATURES = {
    "video", "snapshot", "audio", "audio_talkback", "ptz", "presets",
    "camera_settings_read", "local_recording", "sd_capacity", "sd_recordings",
    "motion_events", "privacy",
}
_SUPPORT_IMPLEMENTATIONS = {"absent", "research", "implemented"}
_SUPPORT_VALIDATIONS = {"none", "offline", "device", "home_assistant"}
_SUPPORT_PATHS = {"camera_lan", "cloud", "nvr_local", "unknown"}
_SUPPORT_AVAILABILITIES = {"available", "unavailable", "unknown"}
_SUPPORT_REASONS = {
    "auth_required", "wrong_region_or_empty_account", "unsupported_model",
    "unsupported_transport", "unverified_firmware", "channel_unverified",
    "timeout", "stale", "lan_unreachable", "stream_timeout",
    "codec_unsupported", "backend_unavailable", "storage_error", "api_incompatible",
}
_SUPPORT_CLOUD_OPERATIONS = {
    "account_auth", "inventory", "new_session_negotiation", "settings_read",
}
_SAFE_EVIDENCE_REF = re.compile(r"^[A-Za-z0-9._/#-]{0,160}$")


def _parse_camera_support(value: object) -> dict | None:
    """Parse only the known optional evidence schema; ignore it on old/future data."""
    if not isinstance(value, dict) or type(value.get("schema_version")) is not int or value["schema_version"] != 1:
        return None
    features = value.get("features")
    if not isinstance(features, dict):
        return None
    parsed = {}
    for name, detail in features.items():
        if name not in _SUPPORT_FEATURES or not isinstance(detail, dict):
            continue
        implementation = detail.get("implementation")
        validation = detail.get("validation")
        path = detail.get("path")
        availability = detail.get("availability", "unknown")
        reason = detail.get("reason", "")
        requires_cloud = detail.get("requires_cloud", [])
        evidence_ref = detail.get("evidence_ref", "")
        tested_firmware = detail.get("tested_firmware", "")
        if (
            not isinstance(implementation, str)
            or implementation not in _SUPPORT_IMPLEMENTATIONS
            or not isinstance(validation, str)
            or validation not in _SUPPORT_VALIDATIONS
            or not isinstance(path, str)
            or path not in _SUPPORT_PATHS
            or not isinstance(availability, str)
            or availability not in _SUPPORT_AVAILABILITIES
            or not isinstance(reason, str)
            or (reason and reason not in _SUPPORT_REASONS)
            or (availability == "available" and reason)
            or (availability == "unavailable" and not reason)
            or not isinstance(requires_cloud, list)
            or len(requires_cloud) > 8
            or any(not isinstance(item, str) or item not in _SUPPORT_CLOUD_OPERATIONS for item in requires_cloud)
            or not isinstance(evidence_ref, str)
            or not _SAFE_EVIDENCE_REF.fullmatch(evidence_ref)
            or not isinstance(tested_firmware, str)
            or len(tested_firmware) > 80
            or (tested_firmware and not re.fullmatch(r"[A-Za-z0-9._+-]{1,80}", tested_firmware))
        ):
            continue
        parsed[name] = {
            "implementation": implementation,
            "validation": validation,
            "path": path,
            "availability": availability,
            **({"reason": reason} if reason else {}),
            "requires_cloud": list(requires_cloud),
            **({"evidence_ref": evidence_ref} if evidence_ref else {}),
            **({"tested_firmware": tested_firmware} if tested_firmware else {}),
        }
    return {"schema_version": 1, "features": parsed} if parsed else None

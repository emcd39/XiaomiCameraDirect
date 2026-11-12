"""Async gateway client, using Home Assistant's shared session."""
from __future__ import annotations
import json
from aiohttp import ClientResponseError, ClientSession, ClientError, ClientTimeout
from urllib.parse import quote, urlsplit


class GatewayError(Exception):
    """Error communicating with the gateway."""


class GatewayUnauthorized(GatewayError):
    """Authentication rejected."""


class GatewayEndpointUnavailable(GatewayError):
    """The managed API is absent, so a legacy read may be used."""


class GatewayClient:
    def __init__(self, session: ClientSession, base_url: str, token: str) -> None:
        self._session = session
        self._url = base_url.rstrip("/")
        self._token = token

    async def _call(self, method: str, path: str, payload: dict | None = None):
        try:
            async with self._session.request(
                method, self._url + path,
                headers={"Authorization": f"Bearer {self._token}"},
                json=payload,
                timeout=ClientTimeout(total=15),
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
        try:
            result = await self._call("GET", "/v2/cameras")
        except GatewayEndpointUnavailable:
            return await self._legacy_cameras()
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
            source = None
            for stream in streams:
                if not isinstance(stream, dict):
                    raise GatewayError("Invalid camera stream response")
                if stream.get("protocol") == "rtsp":
                    source = stream.get("url")
                    if not isinstance(source, str) or not _valid_rtsp_url(source):
                        raise GatewayError("Invalid camera stream response")
                    break
            normalized.append({
                "id": camera_id,
                "name": name,
                "model": camera.get("model", "Unknown"),
                "rtsp_url": source,
                "ptz": capabilities["ptz"],
            })
        if len({camera["id"] for camera in normalized}) != len(normalized):
            raise GatewayError("Invalid camera inventory response")
        return normalized

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

    async def ptz_step(self, camera_id: str, direction: str) -> None:
        await self._call("POST", f"/v1/cameras/{camera_id}/ptz/step", {"direction": direction})

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


def _valid_rtsp_url(value: str) -> bool:
    try:
        parsed = urlsplit(value)
        return parsed.scheme == "rtsp" and bool(parsed.hostname) and parsed.username is None and parsed.password is None and not parsed.query and not parsed.fragment
    except ValueError:
        return False

"""Async gateway client, using Home Assistant's shared session."""
from __future__ import annotations
from aiohttp import ClientResponseError, ClientSession, ClientError, ClientTimeout


class GatewayError(Exception):
    """Error communicating with the gateway."""


class GatewayUnauthorized(GatewayError):
    """Authentication rejected."""


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
                timeout=ClientTimeout(total=10),
            ) as response:
                if response.status == 401:
                    raise GatewayUnauthorized("Gateway rejected API token")
                response.raise_for_status()
                return await response.json()
        except GatewayUnauthorized:
            raise
        except (ClientResponseError, ClientError, TimeoutError) as err:
            # Don't log a full upstream URL: the config may eventually contain secrets.
            raise GatewayError(f"Gateway request failed ({type(err).__name__})") from err

    async def cameras(self) -> list[dict]:
        result = await self._call("GET", "/v1/cameras")
        cameras = result.get("cameras")
        if not isinstance(cameras, list):
            raise GatewayError("Invalid camera inventory response")
        return cameras

    async def ptz_step(self, camera_id: str, direction: str) -> None:
        await self._call("POST", f"/v1/cameras/{camera_id}/ptz/step", {"direction": direction})

    async def ptz_stop(self, camera_id: str) -> None:
        await self._call("POST", f"/v1/cameras/{camera_id}/ptz/stop", {})

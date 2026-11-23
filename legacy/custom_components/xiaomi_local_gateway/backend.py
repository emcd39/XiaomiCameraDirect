"""Backend contracts and the adapter for the external Xiaomi gateway."""

from __future__ import annotations

from typing import Protocol, runtime_checkable


Camera = dict[str, object]


@runtime_checkable
class CameraBackend(Protocol):
    """Minimum operations consumed by the HA camera and PTZ platforms."""

    async def cameras(self) -> list[Camera]: ...

    async def camera_stream(self, camera_id: str) -> str: ...

    async def ptz_step(self, camera_id: str, direction: str, *, managed: bool = False) -> None: ...

    async def close(self) -> None: ...


@runtime_checkable
class CameraReadBackend(Protocol):
    """Optional read-only diagnostics supported by some backends."""

    async def camera_read_capabilities(self) -> set[str]: ...

    async def camera_settings(self, camera_id: str) -> dict: ...

    async def camera_storage(self, camera_id: str) -> dict: ...


class GatewayBackendAdapter:
    """Expose GatewayClient through the backend contract used by HA."""

    def __init__(self, client) -> None:
        self._client = client

    async def cameras(self) -> list[Camera]:
        return await self._client.cameras()

    async def camera_stream(self, camera_id: str) -> str:
        return await self._client.camera_stream(camera_id)

    async def ptz_step(self, camera_id: str, direction: str, *, managed: bool = False) -> None:
        await self._client.ptz_step(camera_id, direction, managed=managed)

    async def close(self) -> None:
        """GatewayClient uses HA's shared HTTP session, so there is nothing to close."""

    async def camera_read_capabilities(self) -> set[str]:
        return await self._client.camera_read_capabilities()

    async def camera_settings(self, camera_id: str) -> dict:
        return await self._client.camera_settings(camera_id)

    async def camera_storage(self, camera_id: str) -> dict:
        return await self._client.camera_storage(camera_id)

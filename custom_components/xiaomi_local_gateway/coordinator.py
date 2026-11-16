"""Gateway camera inventory coordinator."""
from __future__ import annotations
import asyncio
from datetime import timedelta
import logging
from homeassistant.core import callback
from homeassistant.helpers.event import async_track_time_interval
from homeassistant.helpers.update_coordinator import DataUpdateCoordinator, UpdateFailed
from .api import GatewayClient, GatewayError

LOGGER = logging.getLogger(__name__)

class GatewayCoordinator(DataUpdateCoordinator[dict[str, dict]]):
    def __init__(self, hass, entry, client: GatewayClient) -> None:
        super().__init__(hass, LOGGER, name="Xiaomi Local Gateway", config_entry=entry,
                         update_interval=timedelta(seconds=30), always_update=False)
        self.client = client
        self.settings_coordinator = None
        self.storage_coordinator = None

    async def _async_update_data(self) -> dict[str, dict]:
        try:
            items = await self.client.cameras()
        except GatewayError as err:
            raise UpdateFailed(str(err)) from err
        return {item["id"]: item for item in items}


class CameraSettingsCoordinator(DataUpdateCoordinator[dict[str, dict | None]]):
    """Poll the C201's fixed read-only settings separately from inventory."""

    def __init__(self, hass, entry, client: GatewayClient, cameras: GatewayCoordinator) -> None:
        super().__init__(hass, LOGGER, name="Xiaomi Camera Settings", config_entry=entry,
                         update_interval=timedelta(minutes=5))
        self.client = client
        self.cameras = cameras

    async def _async_update_data(self) -> dict[str, dict | None]:
        values: dict[str, dict | None] = {}
        for camera_id, camera in self.cameras.data.items():
            if camera.get("model") != "xiaomi.camera.c201":
                continue
            try:
                values[camera_id] = await self.client.camera_settings(camera_id)
            except GatewayError as err:
                LOGGER.warning("Unable to refresh read-only camera settings (%s)", type(err).__name__)
                values[camera_id] = None
        return values


class CameraStorageCoordinator(DataUpdateCoordinator[dict[str, dict | None]]):
    """Poll raw C201 SD properties without guessing their units or meaning."""

    def __init__(self, hass, entry, client: GatewayClient, cameras: GatewayCoordinator) -> None:
        super().__init__(hass, LOGGER, name="Xiaomi Camera Storage", config_entry=entry,
                         update_interval=timedelta(minutes=5))
        self.client = client
        self.cameras = cameras
        self._storage_lock = asyncio.Lock()
        self._camera_refresh_tasks: dict[str, asyncio.Task] = {}

    async def _async_refresh(self, *args, **kwargs) -> None:
        """Serialize manual and scheduled polls through snapshot publication.

        Home Assistant 2026.10.4's timer handler calls `_async_refresh`
        directly, bypassing the public `async_refresh` method.
        """
        async with self._storage_lock:
            await super()._async_refresh(*args, **kwargs)

    async def _async_update_data(self) -> dict[str, dict | None]:
        values: dict[str, dict | None] = {}
        # Inventory may change while a storage request is waiting on a slower
        # camera. Iterate a snapshot; a return during this poll is queued by
        # request_missing_camera and handled after async_refresh publishes it.
        cameras = tuple(self.cameras.data.items())
        for camera_id, camera in cameras:
            if camera.get("model") != "xiaomi.camera.c201":
                continue
            try:
                values[camera_id] = await self.client.camera_storage(camera_id)
            except GatewayError as err:
                LOGGER.warning("Unable to refresh read-only camera storage (%s)", type(err).__name__)
                values[camera_id] = None
        return values

    def request_missing_camera(self, camera_id: str) -> asyncio.Task | None:
        """Refresh a C201 that returned after a storage poll omitted it."""
        camera = self.cameras.data.get(camera_id)
        if (not self.cameras.last_update_success
                or camera is None
                or camera.get("model") != "xiaomi.camera.c201"):
            return None
        # During a poll, self.data is still the previous snapshot. Queue a
        # post-publication check even when that stale snapshot has this ID.
        if not self._storage_lock.locked() and camera_id in (self.data or {}):
            return None
        task = self._camera_refresh_tasks.get(camera_id)
        if task is None or task.done():
            task = self.hass.async_create_task(self._async_refresh_camera(camera_id))
            self._camera_refresh_tasks[camera_id] = task
        return task

    async def _async_refresh_camera(self, camera_id: str) -> None:
        async with self._storage_lock:
            camera = self.cameras.data.get(camera_id)
            if (not self.cameras.last_update_success
                    or camera is None
                    or camera.get("model") != "xiaomi.camera.c201"
                    or camera_id in (self.data or {})):
                return
            try:
                snapshot = await self.client.camera_storage(camera_id)
            except GatewayError as err:
                LOGGER.warning("Unable to refresh returned camera storage (%s)", type(err).__name__)
                snapshot = None
            values = dict(self.data or {})
            values[camera_id] = snapshot
            self.async_set_updated_data(values)


class CameraSettingsSupport:
    """Discover optional read-only feature flags in one background info poll."""

    def __init__(self, hass, entry, client: GatewayClient, cameras: GatewayCoordinator) -> None:
        self.hass = hass
        self.entry = entry
        self.client = client
        self.cameras = cameras
        self._cancel_retry = None
        self._task = None

    def start(self) -> None:
        self._cancel_retry = async_track_time_interval(
            self.hass, self._schedule_check, timedelta(seconds=60)
        )
        self._schedule_check()

    @callback
    def _schedule_check(self, _now=None) -> None:
        if (self.cameras.settings_coordinator is not None
                and self.cameras.storage_coordinator is not None):
            return
        if self._task is None or self._task.done():
            self._task = self.hass.async_create_task(self.async_check())

    async def async_check(self) -> bool:
        """Create coordinators only for advertised read-only feature flags."""
        if (self.cameras.settings_coordinator is not None
                and self.cameras.storage_coordinator is not None):
            return True
        try:
            features = await self.client.camera_read_capabilities()
        except GatewayError as err:
            LOGGER.warning("Unable to check optional camera read support (%s)", type(err).__name__)
            return False
        discovered = False
        if "camera_settings_read" in features and self.cameras.settings_coordinator is None:
            settings = CameraSettingsCoordinator(self.hass, self.entry, self.client, self.cameras)
            await settings.async_refresh()
            self.cameras.settings_coordinator = settings
            discovered = True
            # Register the settings entities as soon as their first snapshot is
            # ready. A slow storage read below must not hold back these sensors.
            self.cameras.async_update_listeners()
        if "sd_capacity" in features and self.cameras.storage_coordinator is None:
            storage = CameraStorageCoordinator(self.hass, self.entry, self.client, self.cameras)
            await storage.async_refresh()
            self.cameras.storage_coordinator = storage
            discovered = True
            self.cameras.async_update_listeners()
        if (self.cameras.settings_coordinator is not None
                and self.cameras.storage_coordinator is not None
                and self._cancel_retry is not None):
            self._cancel_retry()
            self._cancel_retry = None
        return discovered or bool(features)

    def stop(self) -> None:
        if self._cancel_retry is not None:
            self._cancel_retry()
            self._cancel_retry = None
        if self._task is not None and not self._task.done():
            self._task.cancel()

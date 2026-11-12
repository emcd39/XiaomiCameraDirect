"""Gateway camera inventory coordinator."""
from __future__ import annotations
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
                         update_interval=timedelta(seconds=30))
        self.client = client
        self.settings_coordinator = None

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


class CameraSettingsSupport:
    """Discover optional settings support in the background and retry it."""

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
        if self.cameras.settings_coordinator is not None:
            return
        if self._task is None or self._task.done():
            self._task = self.hass.async_create_task(self.async_check())

    async def async_check(self) -> bool:
        """Return true once settings support is enabled for this entry."""
        if self.cameras.settings_coordinator is not None:
            return True
        try:
            supported = await self.client.supports_camera_settings()
        except GatewayError as err:
            LOGGER.warning("Unable to check optional camera settings support (%s)", type(err).__name__)
            return False
        if not supported:
            return False

        settings = CameraSettingsCoordinator(self.hass, self.entry, self.client, self.cameras)
        await settings.async_refresh()
        self.cameras.settings_coordinator = settings
        self.cameras.async_update_listeners()
        if self._cancel_retry is not None:
            self._cancel_retry()
            self._cancel_retry = None
        return True

    def stop(self) -> None:
        if self._cancel_retry is not None:
            self._cancel_retry()
            self._cancel_retry = None
        if self._task is not None and not self._task.done():
            self._task.cancel()

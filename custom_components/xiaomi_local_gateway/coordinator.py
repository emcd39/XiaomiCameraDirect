"""Gateway camera inventory coordinator."""
from __future__ import annotations
from datetime import timedelta
import logging
from homeassistant.helpers.update_coordinator import DataUpdateCoordinator, UpdateFailed
from .api import GatewayClient, GatewayError

LOGGER = logging.getLogger(__name__)

class GatewayCoordinator(DataUpdateCoordinator[dict[str, dict]]):
    def __init__(self, hass, entry, client: GatewayClient) -> None:
        super().__init__(hass, LOGGER, name="Xiaomi Local Gateway", config_entry=entry,
                         update_interval=timedelta(seconds=30))
        self.client = client

    async def _async_update_data(self) -> dict[str, dict]:
        try:
            items = await self.client.cameras()
        except GatewayError as err:
            raise UpdateFailed(str(err)) from err
        return {item["id"]: item for item in items}

"""Common entity metadata; the camera inventory is config-driven."""
from homeassistant.helpers.update_coordinator import CoordinatorEntity
from .const import DOMAIN

class GatewayEntity(CoordinatorEntity):
    _attr_has_entity_name = True
    def __init__(self, coordinator, did: str):
        super().__init__(coordinator)
        self.did=did

    @property
    def device_info(self):
        camera = self.coordinator.data.get(self.did, {})
        return {
            "identifiers": {(DOMAIN, self.did)},
            "name": camera.get("name", self.did),
            "manufacturer": "Xiaomi",
            "model": camera.get("model", "Unknown"),
        }

    @property
    def available(self):
        return self.coordinator.last_update_success and self.did in self.coordinator.data

"""Authenticated Home Assistant WebSocket command for the card's PTZ pad."""

from homeassistant.components import websocket_api
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers import device_registry as dr, entity_registry as er
import probatio

from .const import DOMAIN, PTZ_OPERATIONS


_STEP = "xiaomi_direct_pilot/ptz/step"


def ptz_control(hass, entity_id):
    """Return the PTZ control bound to a camera entity, if that model has one."""
    entity = er.async_get(hass).async_get(entity_id)
    if entity is None or entity.domain != "camera" or not entity.device_id:
        return None
    device = dr.async_get(hass).async_get(entity.device_id)
    if device is None:
        return None
    entries = hass.config_entries.async_loaded_entries(DOMAIN)
    for domain, device_id in device.identifiers:
        if domain != DOMAIN:
            continue
        for entry in entries:
            runtime = getattr(entry, "runtime_data", None)
            control = getattr(runtime, "ptz", {}).get(device_id)
            if control is not None:
                return control
    return None


@websocket_api.websocket_command({
    probatio.Required("type"): _STEP,
    probatio.Required("entity_id"): str,
    probatio.Required("direction"): str,
})
@websocket_api.async_response
async def websocket_ptz_step(hass, connection, msg):
    """Move one step on the camera's already-open local session."""
    direction = msg["direction"]
    if direction not in PTZ_OPERATIONS:
        connection.send_error(
            msg["id"], "invalid_format", "Direction must be up, down, left or right",
        )
        return
    control = ptz_control(hass, msg["entity_id"])
    if control is None:
        connection.send_error(
            msg["id"], "ptz_unavailable", "This camera has no mapped directional control",
        )
        return
    try:
        await control.press(direction)
    except HomeAssistantError as error:
        connection.send_error(msg["id"], "ptz_failed", str(error))
        return
    connection.send_result(msg["id"])


def register_ptz_commands(hass):
    websocket_api.async_register_command(hass, websocket_ptz_step)

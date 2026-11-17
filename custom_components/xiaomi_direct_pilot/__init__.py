"""Xiaomi Camera Direct integration for Home Assistant."""

import logging
from dataclasses import dataclass
from aiohttp import ClientSession, DummyCookieJar
from homeassistant.const import EVENT_HOMEASSISTANT_STOP, Platform
from homeassistant.exceptions import ConfigEntryAuthFailed
from .button import PTZ_MODELS, PTZControl
from .const import DOMAIN, NAME
from .inventory import account_cameras, create_account, resolve_cameras
from .xiaomi_direct.account import AccountAuthorizationError, AccountError


PLATFORMS = [Platform.CAMERA, Platform.BUTTON]
_LOGGER = logging.getLogger(__name__)


@dataclass(slots=True)
class DirectRuntime:
    client: ClientSession
    account: object
    cameras: list[dict]
    ptz: dict[str, PTZControl]


async def async_migrate_entry(hass, entry):
    if entry.version == 1:
        config = dict(entry.data)
        legacy_id = config.pop("device_id")
        legacy_name = config.pop("name")
        legacy_route = {key: config.pop(key) for key in ("camera_ip", "source_ip", "camera_lan")}
        config["cameras"] = [{
            "device_id": legacy_id, "model": "xiaomi.camera.c201", "name": legacy_name, **legacy_route,
        }]
        hass.config_entries.async_update_entry(
            entry, data=config, version=3, title=NAME,
            unique_id=f'{config["region"]}:{config["account_user_id"]}',
        )
    elif entry.version == 2:
        changes = {"version": 3}
        if entry.title in {"Xiaomi Camera Direct Pilot", "Xiaomi Cameras Direct"}:
            changes["title"] = NAME
        hass.config_entries.async_update_entry(entry, **changes)
    return entry.version == 3


async def async_setup_entry(hass, entry):
    client = ClientSession(cookie_jar=DummyCookieJar())
    account = create_account(client, entry.data)
    saved = {item["device_id"]: item for item in entry.data["cameras"]}
    try:
        try:
            inventory = account_cameras(await account.devices())
        except AccountAuthorizationError as error:
            raise ConfigEntryAuthFailed(str(error)) from None
        except AccountError as error:
            _LOGGER.warning("Direct camera inventory refresh failed: %s", error)
        else:
            if inventory:
                refreshed = {}
                for item in await resolve_cameras(hass, inventory):
                    known = saved.get(item["device_id"])
                    if known and "source_ip" not in item:
                        item = {**item, **{key: known[key] for key in ("camera_ip", "source_ip", "camera_lan") if key in known}}
                    refreshed[item["device_id"]] = item
                saved = refreshed
                data = {**entry.data, "cameras": list(saved.values())}
                if data != entry.data:
                    hass.config_entries.async_update_entry(entry, data=data)
        if (selected := entry.options.get("camera_ids")) is not None:
            saved = {device_id: item for device_id, item in saved.items() if device_id in selected}
        ptz = {
            device_id: PTZControl(hass, device_id)
            for device_id, item in saved.items()
            if item["model"] in PTZ_MODELS and "source_ip" in item
        }
        entry.runtime_data = DirectRuntime(client, account, list(saved.values()), ptz)
        await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
    except BaseException:
        await client.close()
        raise

    async def close_client(_event):
        await client.close()

    entry.async_on_unload(hass.bus.async_listen_once(EVENT_HOMEASSISTANT_STOP, close_client))
    return True


async def async_unload_entry(hass, entry):
    if not await hass.config_entries.async_unload_platforms(entry, PLATFORMS):
        return False
    await entry.runtime_data.client.close()
    return True

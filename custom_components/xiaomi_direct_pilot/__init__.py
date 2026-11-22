"""Xiaomi Camera Direct integration for Home Assistant."""

import logging
from dataclasses import dataclass
from hashlib import sha256
from pathlib import Path
from aiohttp import ClientSession
from homeassistant.components.http import StaticPathConfig
from homeassistant.const import EVENT_HOMEASSISTANT_STOP, Platform
from homeassistant.exceptions import ConfigEntryAuthFailed
from .button import PTZ_MODELS, PTZControl
from .const import C201_MODEL, DOMAIN, NAME
from .inventory import account_cameras, create_account, excluded_camera_ids, merge_refreshed_camera, resolve_cameras
from .settings import C201SettingsCoordinator
from .xiaomi_direct.account import AccountAuthorizationError, AccountError, create_client_session
from .xiaomi_direct.talkback_api import async_stop_talkback_for_sources, register_talkback_commands


PLATFORMS = [Platform.CAMERA, Platform.BUTTON, Platform.SENSOR, Platform.BINARY_SENSOR]
_LOGGER = logging.getLogger(__name__)


@dataclass(slots=True)
class DirectRuntime:
    client: ClientSession
    account: object
    cameras: list[dict]
    ptz: dict[str, PTZControl]
    media_sources: dict[str, object]
    settings: dict[str, C201SettingsCoordinator]


async def async_setup(hass, config):
    """Register the small push-to-talk card and authenticated WS commands."""
    data = hass.data.setdefault(DOMAIN, {})
    data.setdefault("talkback_media", {})
    data.setdefault("talkback_sessions", {})
    register_talkback_commands(hass)

    card_path = Path(__file__).parent / "www" / "xiaomi-direct-camera.js"
    await hass.http.async_register_static_paths([
        StaticPathConfig("/xiaomi-direct/xiaomi-direct-camera.js", str(card_path), True)
    ])
    resources = hass.data["lovelace"].resources
    await resources.async_get_info()
    resource_url = "/xiaomi-direct/xiaomi-direct-camera.js"
    card_bytes = await hass.async_add_executor_job(card_path.read_bytes)
    revision = sha256(card_bytes).hexdigest()[:16]
    versioned_url = f"{resource_url}?v={revision}"
    for resource in resources.async_items():
        if resource.get("url", "").partition("?")[0] == resource_url:
            if resource["url"] != versioned_url:
                await resources.async_update_item(
                    resource["id"], {"res_type": "module", "url": versioned_url},
                )
            break
    else:
        await resources.async_create_item({"res_type": "module", "url": versioned_url})
    return True


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
    client = create_client_session()
    account = create_account(client, entry.data)
    saved = {item["device_id"]: item for item in entry.data["cameras"]}
    excluded = excluded_camera_ids(saved.values(), entry.options)
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
                    device_id = item["device_id"]
                    refreshed[device_id] = merge_refreshed_camera(item, saved.get(device_id))
                saved = refreshed
                data = {**entry.data, "cameras": list(saved.values())}
                changes = {}
                if data != entry.data:
                    changes["data"] = data
                if "camera_ids" in entry.options and "excluded_camera_ids" not in entry.options:
                    changes["options"] = {**entry.options, "excluded_camera_ids": sorted(excluded)}
                if changes:
                    hass.config_entries.async_update_entry(entry, **changes)
        saved = {device_id: item for device_id, item in saved.items() if device_id not in excluded}
        ptz = {
            device_id: PTZControl(hass, device_id)
            for device_id, item in saved.items()
            if item["model"] in PTZ_MODELS and "source_ip" in item
        }
        settings = {
            device_id: C201SettingsCoordinator(hass, entry, account, device_id)
            for device_id, item in saved.items()
            if item["model"] == C201_MODEL
        }
        entry.runtime_data = DirectRuntime(client, account, list(saved.values()), ptz, {}, settings)
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
    media_sources = hass.data.get(DOMAIN, {}).get("talkback_media", {})
    await async_stop_talkback_for_sources(hass, entry.runtime_data.media_sources.values())
    for unique_id, source in entry.runtime_data.media_sources.items():
        media_sources.pop(unique_id, None)
        await source.close()
    await entry.runtime_data.client.close()
    return True

"""One Xiaomi account can expose multiple direct camera entities."""

import asyncio
from ipaddress import IPv4Address, IPv4Network
from pathlib import Path
import re
import socket
import subprocess
import sys

from aiohttp import ClientSession, DummyCookieJar
from homeassistant.components.network import async_get_adapters
from homeassistant.helpers.device_registry import DeviceInfo

from .const import C400_MODEL, DOMAIN
from .xiaomi_direct.account import XiaomiAccount
from .xiaomi_direct.cs2_tcp import CS2Error, validate_lan


QUALITY = {
    "xiaomi.camera.c201": 0,
    C400_MODEL: 3,
    "chuangmi.camera.026c02": 2,
    "mxiang.camera.mod13": 0,
}
_NEIGHBOR = re.compile(r"\b((?:\d{1,3}\.){3}\d{1,3})\b.*?\b((?:[0-9a-f]{2}[:-]){5}[0-9a-f]{2})\b", re.I)


def camera_device_info(name, device_id, model):
    return DeviceInfo(identifiers={(DOMAIN, device_id)}, name=name, model=model, manufacturer="Xiaomi")


def _mac_key(value):
    if not isinstance(value, str):
        return ""
    raw = value.lower().replace("-", "").replace(":", "")
    return raw if re.fullmatch(r"[0-9a-f]{12}", raw) and raw != "000000000000" else ""


def _neighbors():
    try:
        if sys.platform == "win32":
            table = subprocess.run(
                ["arp", "-a"], capture_output=True, text=True, errors="replace", timeout=3, check=True,
                creationflags=subprocess.CREATE_NO_WINDOW,
            ).stdout
        elif sys.platform.startswith("linux"):
            table = Path("/proc/net/arp").read_text(encoding="ascii", errors="replace")
        else:
            return {}
    except (OSError, subprocess.SubprocessError):
        return {}
    addresses = {}
    for line in table.splitlines():
        if match := _NEIGHBOR.search(line):
            ip, mac = match.groups()
            addresses.setdefault(_mac_key(mac), set()).add(ip)
    return addresses


def _route_source(camera):
    # UDP connect asks the kernel for its route without sending a datagram.
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as route:
        route.connect((str(camera), 32108))
        return IPv4Address(route.getsockname()[0])


async def local_route(hass, text):
    try:
        camera = IPv4Address(text.strip())
        source = await hass.async_add_executor_job(_route_source, camera)
        adapters = await async_get_adapters(hass)
    except (AttributeError, ValueError, OSError):
        return None
    for adapter in adapters:
        for address in adapter["ipv4"]:
            if address["address"] != str(source):
                continue
            lan = IPv4Network((source, address["network_prefix"]), strict=False)
            try:
                validate_lan(camera, source, lan)
            except CS2Error:
                continue
            return str(camera), str(source), str(lan)
    return None


def account_cameras(reply):
    cameras = {}
    devices = reply.get("list") if isinstance(reply, dict) else None
    if not isinstance(devices, list):
        return cameras
    for device in devices:
        if not isinstance(device, dict):
            continue
        model = device.get("model")
        device_id = device.get("did")
        if not isinstance(model, str) or not (".camera." in model or ".cateye." in model):
            continue
        if not isinstance(device_id, str) or not device_id:
            continue
        name, address = device.get("name"), device.get("localip")
        cameras[device_id] = {
            "device_id": device_id,
            "model": model,
            "name": f"{name.strip() if isinstance(name, str) and name.strip() else model} Direct",
            "camera_ip": address.strip() if isinstance(address, str) else "",
            "mac": _mac_key(device.get("mac")),
        }
    return cameras


async def load_account_cameras(config):
    async with ClientSession(cookie_jar=DummyCookieJar()) as client:
        account = XiaomiAccount(
            client, region=config["region"],
            user_id=config["account_user_id"], pass_token=config["account_pass_token"],
        )
        return account_cameras(await account.devices())


def create_account(client, config):
    return XiaomiAccount(
        client, region=config["region"],
        user_id=config["account_user_id"], pass_token=config["account_pass_token"],
    )


async def resolve_cameras(hass, cameras):
    neighbors = await hass.async_add_executor_job(_neighbors)

    async def resolve(camera):
        candidates = neighbors.get(camera["mac"], set()) if camera["mac"] else set()
        if len(candidates) == 1 and (route := await local_route(hass, next(iter(candidates)))):
            return route
        return await local_route(hass, camera["camera_ip"])

    routes = await asyncio.gather(*(resolve(item) for item in cameras.values()))
    result = []
    for camera, route in zip(cameras.values(), routes, strict=True):
        item = dict(camera)
        item.pop("mac")
        if route:
            item["camera_ip"], item["source_ip"], item["camera_lan"] = route
        result.append(item)
    return result

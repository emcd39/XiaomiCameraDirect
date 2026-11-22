"""Adapter that runs the direct Xiaomi backend behind the gateway HA entities."""

from __future__ import annotations

from homeassistant.exceptions import ConfigEntryAuthFailed

from .api import GatewayError
from .backend import Camera

class DirectBackendAdapter:
    """Use the same inventory, camera and PTZ entity contract as GatewayClient."""

    def __init__(self, client, account, cameras, media, controls, statuses=None):
        self._client = client
        self._account = account
        self._cameras = {item["device_id"]: item for item in cameras}
        self._media = media
        self._controls = controls
        self._statuses = statuses or {}

    @classmethod
    async def create(cls, hass, entry):
        # Import only when the user selects the direct backend. The direct
        # transport remains independent of the gateway integration at runtime.
        from custom_components.xiaomi_direct_pilot.camera import _video_opener, camera_stream_settings
        from custom_components.xiaomi_direct_pilot.const import (
            C400_MODEL,
        )
        from custom_components.xiaomi_direct_pilot.inventory import (
            QUALITY, account_cameras, create_account, resolve_cameras,
        )
        from custom_components.xiaomi_direct_pilot.xiaomi_direct.account import (
            AccountAuthorizationError, AccountError, create_client_session,
        )
        from custom_components.xiaomi_direct_pilot.xiaomi_direct.ha_media import LocalMediaSource
        from custom_components.xiaomi_direct_pilot.xiaomi_direct.cs2_tcp import CS2Error, validate_lan
        from custom_components.xiaomi_direct_pilot.button import PTZControl, PTZ_MODELS
        from ipaddress import IPv4Address, IPv4Network

        options = entry.options
        config = {
            "region": options["direct_region"],
            "account_user_id": options["direct_account_user_id"],
            "account_pass_token": options["direct_account_pass_token"],
        }
        client = create_client_session()
        account = create_account(client, config)
        try:
            try:
                inventory = account_cameras(await account.devices())
            except AccountAuthorizationError as err:
                entry.async_start_reauth(hass)
                raise ConfigEntryAuthFailed(str(err)) from None
            except AccountError as err:
                raise GatewayError("Direct Xiaomi inventory could not be refreshed") from err
            if not inventory:
                raise GatewayError("Direct Xiaomi account returned no cameras")

            cameras = await resolve_cameras(hass, inventory)
            selected = set(options.get("direct_camera_ids", ()))
            if "direct_camera_ids" in options:
                if "direct_cameras" in options:
                    known_ids = {item["device_id"] for item in options["direct_cameras"]}
                    excluded = known_ids - selected
                    cameras = [item for item in cameras if item["device_id"] not in excluded]
                else:
                    cameras = [item for item in cameras if item["device_id"] in selected]
            id_map = options.get("direct_camera_id_map", {})
            cameras = [
                {**item, "backend_id": id_map.get(item["device_id"], item["device_id"])}
                for item in cameras
            ]
            backend_ids = [item["backend_id"] for item in cameras]
            if len(set(backend_ids)) != len(backend_ids):
                raise GatewayError("Direct camera entity ID mapping contains duplicates")
            media, controls, statuses = {}, {}, {}
            for item in cameras:
                device_id, backend_id, model = item["device_id"], item["backend_id"], item["model"]
                if model not in QUALITY:
                    statuses[device_id] = "model_profile_pending"
                elif not item.get("camera_ip"):
                    statuses[device_id] = "missing_camera_ip"
                elif not item.get("source_ip") or not item.get("camera_lan"):
                    statuses[device_id] = "no_current_route"
                else:
                    statuses[device_id] = "ready"
                control = None
                if model in PTZ_MODELS and all(
                    item.get(key) for key in ("camera_ip", "source_ip", "camera_lan")
                ):
                    control = controls[backend_id] = PTZControl(hass, device_id)
                quality, audio = camera_stream_settings(model, device_id, config["region"], options)
                if quality is None or not all(item.get(key) for key in ("camera_ip", "source_ip", "camera_lan")):
                    continue
                camera_config = {**config, **item}
                camera_config["camera_ip"] = IPv4Address(item["camera_ip"])
                camera_config["source_ip"] = IPv4Address(item["source_ip"])
                camera_config["camera_lan"] = IPv4Network(item["camera_lan"])
                try:
                    validate_lan(
                        camera_config["camera_ip"], camera_config["source_ip"], camera_config["camera_lan"],
                    )
                except (CS2Error, ValueError) as err:
                    raise GatewayError("Direct camera route is invalid") from err
                opener = _video_opener(
                    hass, camera_config, account, control, quality=quality, entry=entry, audio=audio,
                )
                media[backend_id] = LocalMediaSource(
                    opener, label=model,
                    attempts=2 if model in {C400_MODEL, "mxiang.camera.mod13"} else 1,
                    audio=audio,
                    talkback=(model == "chuangmi.camera.079ae2" and audio),
                )
            return cls(client, account, cameras, media, controls, statuses)
        except BaseException:
            await client.close()
            raise

    async def cameras(self) -> list[Camera]:
        return [
            {
                "id": item.get("backend_id", item["device_id"]),
                "name": item["name"],
                "model": item["model"],
                "ptz": item.get("backend_id", item["device_id"]) in self._controls,
                "on_demand_stream": item.get("backend_id", item["device_id"]) in self._media,
                "available": item.get("backend_id", item["device_id"]) in self._media,
                "direct_status": self._statuses.get(
                    item["device_id"],
                    "ready" if item.get("backend_id", item["device_id"]) in self._media else "no_current_route",
                ),
            }
            for item in self._cameras.values()
        ]

    async def camera_stream(self, camera_id: str) -> str:
        source = self._media.get(camera_id)
        if source is None:
            raise GatewayError("Direct camera has no current local route")
        if source.url is None:
            await source.start()
        return source.url

    async def ptz_step(self, camera_id: str, direction: str, *, managed: bool = False) -> None:
        control = self._controls.get(camera_id)
        if control is None:
            raise GatewayError("Direct PTZ is unsupported for this camera")
        await control.press(direction)

    async def camera_read_capabilities(self) -> set[str]:
        return set()

    async def camera_settings(self, camera_id: str) -> dict:
        raise GatewayError("Direct backend does not expose gateway camera settings")

    async def camera_storage(self, camera_id: str) -> dict:
        raise GatewayError("Direct backend does not expose gateway camera storage")

    async def close(self) -> None:
        for source in self._media.values():
            await source.close()
        await self._client.close()

"""Expose Xiaomi cameras through Home Assistant's native HLS and snapshot services."""

from contextlib import asynccontextmanager
from ipaddress import IPv4Address, IPv4Network

from aiohttp import ClientSession, DummyCookieJar
from homeassistant.components.camera import Camera, CameraEntityFeature, PLATFORM_SCHEMA
from homeassistant.const import EVENT_HOMEASSISTANT_STOP
from homeassistant.helpers import config_validation as cv
import voluptuous as vol

from .const import C400_LOW_RESOLUTION, C400_MODEL
from .inventory import QUALITY, camera_device_info, create_account
from .xiaomi_direct.account import AccountAuthorizationError
from .xiaomi_direct.c201_session import open_video, request_signed_session
from .xiaomi_direct.cs2_tcp import CS2Error, validate_lan
from .xiaomi_direct.ha_media import LocalMediaSource
from .xiaomi_direct.h264_video import H264Video


PLATFORM_SCHEMA = PLATFORM_SCHEMA.extend({
    vol.Optional("name", default="Xiaomi C201 Direct"): cv.string,
    vol.Required("device_id"): vol.All(cv.string, vol.Length(min=1)),
    vol.Required("camera_ip"): vol.Coerce(IPv4Address),
    vol.Required("source_ip"): vol.Coerce(IPv4Address),
    vol.Required("camera_lan"): vol.Coerce(IPv4Network),
    vol.Required("region"): vol.In(("cn", "de", "i2", "ru", "sg", "us")),
    vol.Required("account_user_id"): cv.string,
    vol.Required("account_pass_token"): cv.string,
})


def _video_opener(hass, config, account=None, control=None, *, quality, entry=None):
    @asynccontextmanager
    async def connect():
        if account is None:
            async with ClientSession(cookie_jar=DummyCookieJar()) as client:
                temporary = create_account(client, config)
                credentials = await request_signed_session(temporary.request, config["device_id"])
        else:
            try:
                credentials = await request_signed_session(account.request, config["device_id"])
            except AccountAuthorizationError:
                entry.async_start_reauth(hass)
                raise
        async with open_video(
            config["camera_ip"], config["source_ip"], config["camera_lan"], credentials,
            quality=quality,
            preference="udp" if config.get("model") == "chuangmi.camera.026c02" else "auto",
        ) as video:
            if control is not None:
                control.set_video(video)
            try:
                yield H264Video(video) if config.get("model", "xiaomi.camera.c201") == "xiaomi.camera.c201" else video
            finally:
                if control is not None:
                    control.set_video(None)

    return connect


async def async_setup_platform(hass, config, async_add_entities, discovery_info=None):
    _add_camera(hass, config, async_add_entities)


async def async_setup_entry(hass, entry, async_add_entities):
    entities = []
    for item in entry.runtime_data.cameras:
        config = {**entry.data, **item}
        media = None
        model = item["model"]
        quality = QUALITY.get(model)
        if (
            model == C400_MODEL and entry.data["region"] == "de"
            and item["device_id"] in entry.options.get(C400_LOW_RESOLUTION, ())
        ):
            quality = 2
        if quality is not None and "source_ip" in item:
            config["camera_ip"] = IPv4Address(item["camera_ip"])
            config["source_ip"] = IPv4Address(item["source_ip"])
            config["camera_lan"] = IPv4Network(item["camera_lan"])
            media = LocalMediaSource(
                _video_opener(
                    hass, config, entry.runtime_data.account, entry.runtime_data.ptz.get(item["device_id"]),
                    quality=quality, entry=entry,
                ),
                label=model,
                attempts=2 if model in {C400_MODEL, "mxiang.camera.mod13"} else 1,
            )
        entities.append(DirectCamera(item["name"], item["device_id"], model, media, quality))
    async_add_entities(entities)


def _add_camera(hass, config, async_add_entities):
    try:
        validate_lan(config["camera_ip"], config["source_ip"], config["camera_lan"])
    except CS2Error as error:
        raise vol.Invalid(str(error)) from None
    quality = QUALITY["xiaomi.camera.c201"]
    async_add_entities([
        DirectCamera(config["name"], config["device_id"], "xiaomi.camera.c201",
                     LocalMediaSource(_video_opener(hass, config, quality=quality)), quality)
    ])


class DirectCamera(Camera):
    def __init__(self, name, device_id, model, media, quality):
        super().__init__()
        self._attr_name = name
        identity = "c201" if model == "xiaomi.camera.c201" else "camera"
        self._attr_unique_id = f"xiaomi_direct_{identity}_{device_id}"
        self._attr_device_info = camera_device_info(name, device_id, model)
        self._media = media
        self._attr_available = media is not None
        self._attr_supported_features = CameraEntityFeature.STREAM if media else CameraEntityFeature(0)
        self._attr_extra_state_attributes = {"model_id": model}
        if quality is not None:
            self._attr_extra_state_attributes["requested_video_profile"] = quality
        if media is None:
            self._attr_extra_state_attributes["direct_status"] = (
                "no_lan_address" if model in QUALITY else "unsupported_model"
            )

    @property
    def use_stream_for_stills(self):
        return True

    async def async_added_to_hass(self):
        if self._media:
            await self._media.start()
            self.async_on_remove(
                self.hass.bus.async_listen_once(EVENT_HOMEASSISTANT_STOP, self._stop_media)
            )
        await super().async_added_to_hass()

    async def _stop_media(self, _event):
        await self._media.close()

    async def async_will_remove_from_hass(self):
        if self._media:
            await self._media.close()
        await super().async_will_remove_from_hass()

    async def stream_source(self):
        return self._media.url if self._media else None

    async def async_create_stream(self):
        stream = await super().async_create_stream()
        if stream is not None:
            # This loopback MPEG-TS source starts on a video keyframe and
            # publishes no audio. The worker needn't probe a long A/V window.
            stream.pyav_options.update(analyzeduration="750000", probesize="524288")
        return stream

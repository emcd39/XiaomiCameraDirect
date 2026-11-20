"""Expose Xiaomi cameras through Home Assistant's native HLS and snapshot services."""

from contextlib import asynccontextmanager
from ipaddress import IPv4Address, IPv4Network

from homeassistant.components.camera import Camera, CameraEntityFeature, PLATFORM_SCHEMA
from homeassistant.const import EVENT_HOMEASSISTANT_STOP
from homeassistant.helpers import config_validation as cv
import voluptuous as vol

from .const import (
    C201_AUDIO, C201_AUDIO_PROFILE, C201_MODEL, C400_LOW_RESOLUTION, C400_MODEL,
    C701_AUDIO, C701_MODEL, DOMAIN,
)
from .inventory import QUALITY, camera_device_info, create_account
from .xiaomi_direct.account import AccountAuthorizationError, create_client_session
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


def camera_stream_settings(model, device_id, region, options):
    """Resolve opt-in media features without changing default profiles."""
    quality = QUALITY.get(model)
    audio = region == "de" and (
        (model == C201_MODEL and device_id in options.get(C201_AUDIO, ()))
        or (model == C701_MODEL and device_id in options.get(C701_AUDIO, ()))
    )
    if model == C201_MODEL and audio:
        quality = C201_AUDIO_PROFILE
    elif (
        model == C400_MODEL and region == "de"
        and device_id in options.get(C400_LOW_RESOLUTION, ())
    ):
        quality = 2
    return quality, audio


def _video_opener(hass, config, account=None, control=None, *, quality, entry=None, audio=False):
    preference = "udp" if config.get("model") == "chuangmi.camera.026c02" else "auto"
    if audio:
        preference = "tcp"

    @asynccontextmanager
    async def connect():
        if account is None:
            async with create_client_session() as client:
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
            audio=audio,
            preference=preference,
        ) as video:
            if control is not None:
                control.set_video(video)
            try:
                yield H264Video(video)
            finally:
                if control is not None:
                    control.set_video(None)

    return connect


async def async_setup_platform(hass, config, async_add_entities, discovery_info=None):
    _add_camera(hass, config, async_add_entities)


async def async_setup_entry(hass, entry, async_add_entities):
    entities = []
    media_sources = hass.data.setdefault(DOMAIN, {}).setdefault("talkback_media", {})
    for item in entry.runtime_data.cameras:
        config = {**entry.data, **item}
        media = None
        model = item["model"]
        quality, audio = camera_stream_settings(
            model, item["device_id"], entry.data["region"], entry.options,
        )
        if quality is not None and "source_ip" in item:
            config["camera_ip"] = IPv4Address(item["camera_ip"])
            config["source_ip"] = IPv4Address(item["source_ip"])
            config["camera_lan"] = IPv4Network(item["camera_lan"])
            media = LocalMediaSource(
                _video_opener(
                    hass, config, entry.runtime_data.account, entry.runtime_data.ptz.get(item["device_id"]),
                    quality=quality, entry=entry, audio=audio,
                ),
                label=model,
                attempts=2 if model in {C400_MODEL, "mxiang.camera.mod13"} else 1,
                audio=audio,
                talkback=(model == C701_MODEL and audio),
            )
        entity = DirectCamera(item["name"], item["device_id"], model, media, quality, audio)
        entities.append(entity)
        if media is not None:
            entry.runtime_data.media_sources[entity.unique_id] = media
            media_sources[entity.unique_id] = media
    async_add_entities(entities)


def _add_camera(hass, config, async_add_entities):
    try:
        validate_lan(config["camera_ip"], config["source_ip"], config["camera_lan"])
    except CS2Error as error:
        raise vol.Invalid(str(error)) from None
    quality = QUALITY[C201_MODEL]
    async_add_entities([
        DirectCamera(config["name"], config["device_id"], C201_MODEL,
                     LocalMediaSource(_video_opener(hass, config, quality=quality)), quality)
    ])


class DirectCamera(Camera):
    def __init__(self, name, device_id, model, media, quality, audio_enabled=False):
        super().__init__()
        self._attr_name = name
        identity = "c201" if model == C201_MODEL else "camera"
        self._attr_unique_id = f"xiaomi_direct_{identity}_{device_id}"
        self._attr_device_info = camera_device_info(name, device_id, model)
        self._media = media
        self._attr_available = media is not None
        self._attr_supported_features = CameraEntityFeature.STREAM if media else CameraEntityFeature(0)
        self._attr_extra_state_attributes = {"model_id": model}
        self._attr_extra_state_attributes["audio_enabled"] = bool(audio_enabled and media is not None)
        self._attr_extra_state_attributes["talkback_enabled"] = bool(
            audio_enabled and model == C701_MODEL and media is not None
        )
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
            # identifies any audio track before sending its first packet.
            stream.pyav_options.update(analyzeduration="750000", probesize="524288")
        return stream

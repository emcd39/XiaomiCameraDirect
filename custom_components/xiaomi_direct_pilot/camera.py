"""Expose Xiaomi cameras through Home Assistant's native HLS and snapshot services."""

from contextlib import asynccontextmanager
from dataclasses import replace
from ipaddress import IPv4Address, IPv4Network

from homeassistant.components.camera import Camera, CameraEntityFeature, PLATFORM_SCHEMA
from homeassistant.const import EVENT_HOMEASSISTANT_STOP
from homeassistant.helpers import config_validation as cv
from homeassistant.helpers.dispatcher import async_dispatcher_connect
import voluptuous as vol

from .const import (
    AW300_AUDIO, AW300_MODEL, C201_AUDIO, C201_AUDIO_PROFILE, C201_MODEL, C400_AUDIO, C400_LOW_RESOLUTION, C400_MODEL,
    C701_AUDIO, C701_MODEL, DOMAIN, PTZ_OPERATIONS,
)
from .inventory import QUALITY, camera_device_info, create_account
from .xiaomi_direct.account import AccountAuthorizationError, create_client_session
from .xiaomi_direct.c201_session import open_video, request_signed_session
from .xiaomi_direct.cs2_tcp import CS2Error, validate_lan
from .xiaomi_direct.ha_media import LocalMediaSource
from .xiaomi_direct.h264_video import H264Video


# The frontend holds back two HLS parts before it plays, so the part length is
# most of the delay between the camera and the picture. Shortening it from the
# one second default brings a step of the PTZ pad close to live.
LOW_LATENCY_PART = 0.3
STREAM_DOMAIN = "stream"
# Mirrors homeassistant.components.stream.const.ATTR_SETTINGS.
STREAM_SETTINGS_KEY = "settings"


PLATFORM_SCHEMA = PLATFORM_SCHEMA.extend({    vol.Optional("name", default="Xiaomi C201 Direct"): cv.string,
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
        or (model == C400_MODEL and device_id in options.get(C400_AUDIO, ()))
        or (model == AW300_MODEL and device_id in options.get(AW300_AUDIO, ()))
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
    model = config.get("model")
    preference = "udp" if model == "chuangmi.camera.026c02" or (model == AW300_MODEL and audio) else "auto"
    if audio and model in {C201_MODEL, C701_MODEL}:
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
        entity = DirectCamera(
            item["name"], item["device_id"], model, media, quality, audio,
            control=entry.runtime_data.ptz.get(item["device_id"]),
        )
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
    def __init__(self, name, device_id, model, media, quality, audio_enabled=False, control=None):
        super().__init__()
        self._attr_name = name
        identity = "c201" if model == C201_MODEL else "camera"
        self._attr_unique_id = f"xiaomi_direct_{identity}_{device_id}"
        self._attr_device_info = camera_device_info(name, device_id, model)
        self._media = media
        self._attr_available = media is not None
        self._attr_supported_features = CameraEntityFeature.STREAM if media else CameraEntityFeature(0)
        self._attr_capability_attributes = {"model_id": model}
        self._attr_extra_state_attributes = {}
        self._attr_extra_state_attributes["audio_enabled"] = bool(audio_enabled and media is not None)
        self._attr_extra_state_attributes["talkback_enabled"] = bool(
            audio_enabled and model == C701_MODEL and media is not None
        )
        # The dashboard card renders its D-pad from these two attributes, so a
        # camera without a mapped driver never shows buttons that cannot work.
        self._ptz = control
        if control is not None:
            self._attr_extra_state_attributes["ptz_directions"] = list(PTZ_OPERATIONS)
            self._attr_extra_state_attributes["ptz_enabled"] = control.video is not None
        if quality is not None:
            self._attr_extra_state_attributes["requested_video_profile"] = quality
        if media is None:
            self._attr_capability_attributes["direct_status"] = (
                "no_lan_address" if model in QUALITY else "model_profile_pending"
            )

    @property
    def use_stream_for_stills(self):
        return True

    def _ptz_state_changed(self):
        self._attr_extra_state_attributes["ptz_enabled"] = self._ptz.video is not None
        self.async_write_ha_state()

    async def async_added_to_hass(self):
        if self._media:
            await self._media.start()
            self.async_on_remove(
                self.hass.bus.async_listen_once(EVENT_HOMEASSISTANT_STOP, self._stop_media)
            )
        if self._ptz is not None:
            self.async_on_remove(async_dispatcher_connect(
                self.hass, self._ptz.signal, self._ptz_state_changed,
            ))
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
        stream = await self._create_low_latency_stream()
        if stream is not None:
            # This loopback MPEG-TS source starts on a video keyframe and
            # identifies any audio track before sending its first packet.
            stream.pyav_options.update(analyzeduration="750000", probesize="524288")
        return stream

    async def _create_low_latency_stream(self):
        """Create the stream with a shorter HLS part than the global default.

        The stream integration keeps one StreamSettings instance for every
        camera and copies it per stream, so the part length can only be
        changed around the call that builds ours. Home Assistant holds back
        two parts before playing, which is the whole delay this camera adds:
        2 s at the default part of 1 s, 0.6 s at 0.3 s.
        """
        stream_data = self.hass.data.get(STREAM_DOMAIN)
        settings = stream_data.get(STREAM_SETTINGS_KEY) if isinstance(stream_data, dict) else None
        current = getattr(settings, "part_target_duration", None)
        if current is None or current <= LOW_LATENCY_PART:
            return await super().async_create_stream()
        try:
            tightened = replace(
                settings,
                part_target_duration=LOW_LATENCY_PART,
                # Home Assistant derives these two from the part length.
                hls_advance_part_limit=max(int(3 / LOW_LATENCY_PART), 3),
                hls_part_timeout=2 * LOW_LATENCY_PART,
            )
        except (TypeError, ValueError):
            return await super().async_create_stream()
        stream_data[STREAM_SETTINGS_KEY] = tightened
        try:
            return await super().async_create_stream()
        finally:
            stream_data[STREAM_SETTINGS_KEY] = settings

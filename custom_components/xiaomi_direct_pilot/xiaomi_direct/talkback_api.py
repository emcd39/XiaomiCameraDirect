"""Authenticated Home Assistant WebSocket commands for camera talkback."""

import base64
import binascii

from homeassistant.components import websocket_api
from homeassistant.helpers import entity_registry as er
import probatio

from ..const import DOMAIN
from .ha_media import TalkbackError


_START = "xiaomi_direct_pilot/talkback/start"
_AUDIO = "xiaomi_direct_pilot/talkback/audio"
_STOP = "xiaomi_direct_pilot/talkback/stop"


def _session_store(hass):
    return hass.data[DOMAIN].setdefault("talkback_sessions", {})


async def _stop_session(hass, token, session=None):
    """Stop and forget a session, including its disconnect cleanup hook."""
    sessions = _session_store(hass)
    active = sessions.pop(token, None)
    if active is None:
        active = session
    if active is None:
        return
    source, connection = active
    connection.subscriptions.pop(token, None)
    await source.stop_talkback(token)


async def async_stop_talkback_for_sources(hass, sources):
    """Stop any camera speaker session before its integration unloads."""
    selected = set(sources)
    sessions = _session_store(hass)
    for token, (source, _connection) in tuple(sessions.items()):
        if source in selected:
            await _stop_session(hass, token)


@websocket_api.websocket_command({
    probatio.Required("type"): _START,
    probatio.Required("entity_id"): str,
})
@websocket_api.async_response
async def websocket_talkback_start(hass, connection, msg):
    """Start one push-to-talk session for an actively streamed camera."""
    entity = er.async_get(hass).async_get(msg["entity_id"])
    source = (
        hass.data[DOMAIN].get("talkback_media", {}).get(entity.unique_id)
        if entity is not None and entity.domain == "camera"
        else None
    )
    if source is None:
        connection.send_error(msg["id"], "talkback_unavailable", "This camera has no enabled talkback source")
        return
    try:
        token = await source.start_talkback()
    except TalkbackError as error:
        connection.send_error(msg["id"], "talkback_unavailable", str(error))
        return
    sessions = _session_store(hass)
    sessions[token] = (source, connection)

    feed = source._feed
    if feed is not None:
        def invalidate(session_token):
            active = sessions.pop(session_token, None)
            if active is not None:
                active[1].subscriptions.pop(session_token, None)

        feed.talkback_invalidate = invalidate

    def cleanup():
        sessions.pop(token, None)
        hass.async_create_task(source.stop_talkback(token))

    connection.subscriptions[token] = cleanup
    connection.send_result(msg["id"], {"session_id": token})


@websocket_api.websocket_command({
    probatio.Required("type"): _AUDIO,
    probatio.Required("session_id"): str,
    probatio.Required("frames"): list,
})
@websocket_api.async_response
async def websocket_talkback_audio(hass, connection, msg):
    """Forward at most four raw Opus frames; sound is never recorded."""
    active = _session_store(hass).get(msg["session_id"])
    frames = msg["frames"]
    if active is None or active[1] is not connection:
        connection.send_error(msg["id"], "talkback_unavailable", "C701 talkback is no longer active")
        return
    if not 1 <= len(frames) <= 4:
        connection.send_error(msg["id"], "invalid_format", "Send one to four Opus frames per message")
        return
    source, _ = active
    try:
        for encoded in frames:
            if not isinstance(encoded, str) or len(encoded) > 5500:
                raise TalkbackError("Invalid Opus audio frame")
            payload = base64.b64decode(encoded, validate=True)
            await source.send_talkback(msg["session_id"], payload)
    except (binascii.Error, TalkbackError) as error:
        await _stop_session(hass, msg["session_id"], active)
        connection.send_error(msg["id"], "talkback_failed", str(error))
        return
    connection.send_result(msg["id"])


@websocket_api.websocket_command({
    probatio.Required("type"): _STOP,
    probatio.Required("session_id"): str,
})
@websocket_api.async_response
async def websocket_talkback_stop(hass, connection, msg):
    """Stop the speaker and release the held microphone session."""
    active = _session_store(hass).get(msg["session_id"])
    if active is not None and active[1] is not connection:
        connection.send_error(msg["id"], "talkback_unavailable", "C701 talkback belongs to another session")
        return
    await _stop_session(hass, msg["session_id"])
    connection.send_result(msg["id"])


def register_talkback_commands(hass):
    websocket_api.async_register_command(hass, websocket_talkback_start)
    websocket_api.async_register_command(hass, websocket_talkback_audio)
    websocket_api.async_register_command(hass, websocket_talkback_stop)

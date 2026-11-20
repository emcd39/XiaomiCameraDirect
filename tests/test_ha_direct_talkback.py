"""Talkback must be opt-in, tied to a live camera feed, and deadman protected."""

import asyncio
import unittest
from types import SimpleNamespace

from custom_components.xiaomi_direct_pilot.xiaomi_direct.ha_media import (
    LocalMediaSource,
    TalkbackError,
    _Feed,
)
from custom_components.xiaomi_direct_pilot.xiaomi_direct.talkback_api import (
    async_stop_talkback_for_sources,
)


class FakeSpeaker:
    def __init__(self):
        self.started = 0
        self.stopped = 0
        self.frames = []

    async def start_speaker(self):
        self.started += 1

    async def write_speaker_audio(self, payload):
        self.frames.append(payload)

    async def stop_speaker(self):
        self.stopped += 1


class TalkbackTest(unittest.IsolatedAsyncioTestCase):
    def active_source(self, *, audio=True):
        source = LocalMediaSource(lambda: None, audio=audio, talkback=audio)
        feed = _Feed()
        feed.subscribers.add(asyncio.Queue())
        feed.video = FakeSpeaker()
        source._feed = feed
        return source, feed

    async def test_talkback_is_gated_on_audio_and_live_video(self):
        disabled, _ = self.active_source(audio=False)
        with self.assertRaisesRegex(TalkbackError, "enabled C701"):
            await disabled.start_talkback()

        idle = LocalMediaSource(lambda: None, audio=True, talkback=True)
        with self.assertRaisesRegex(TalkbackError, "Open the C701 live video"):
            await idle.start_talkback()

    async def test_push_to_talk_forwards_frames_and_stops_once(self):
        source, feed = self.active_source()
        token = await source.start_talkback()
        self.assertEqual(feed.video.started, 1)
        await source.send_talkback(token, b"opus-one")
        await source.stop_talkback(token)
        self.assertEqual(feed.video.frames, [b"opus-one"])
        self.assertEqual(feed.video.stopped, 1)

    async def test_deadman_stops_when_audio_packets_disappear(self):
        source, feed = self.active_source()
        token = await source.start_talkback()
        await source.send_talkback(token, b"opus-one")
        async with asyncio.timeout(2):
            while feed.talkback_token is not None:
                await asyncio.sleep(0.05)
        self.assertEqual(feed.video.stopped, 1)
        with self.assertRaisesRegex(TalkbackError, "no longer active"):
            await source.send_talkback(token, b"opus-two")

    async def test_unload_stops_and_forgets_matching_websocket_sessions(self):
        source, feed = self.active_source()
        token = await source.start_talkback()
        connection = SimpleNamespace(subscriptions={token: lambda: None})
        hass = SimpleNamespace(data={"xiaomi_direct_pilot": {
            "talkback_sessions": {token: (source, connection)},
        }})

        await async_stop_talkback_for_sources(hass, [source])

        self.assertIsNone(feed.talkback_token)
        self.assertEqual(feed.video.stopped, 1)
        self.assertEqual(connection.subscriptions, {})
        self.assertEqual(hass.data["xiaomi_direct_pilot"]["talkback_sessions"], {})

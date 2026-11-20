"""Boot a disposable HA instance and decode its HLS from a simulated C201."""

import asyncio
from contextlib import asynccontextmanager
import io
import logging
from pathlib import Path
import shutil
import socket
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import AsyncMock, patch

from aiohttp import ClientSession
import av
from homeassistant.bootstrap import async_setup_hass
from homeassistant.components.camera import async_request_stream
from homeassistant.components.camera.helper import get_camera_from_entity_id
from homeassistant.data_entry_flow import InvalidData
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers import entity_registry
from homeassistant.runner import RuntimeConfig

from custom_components import xiaomi_direct_pilot
from custom_components.xiaomi_direct_pilot import camera, config_flow
from custom_components.xiaomi_direct_pilot.xiaomi_direct.h264_video import H264Video
from custom_components.xiaomi_direct_pilot.xiaomi_direct.ha_media import LocalMediaSource
from custom_components.xiaomi_direct_pilot.xiaomi_direct.miss_media import AudioPacket, VideoFrame
from test_ha_direct_media_decode import ffmpeg


class DirectHaStreamTest(unittest.IsolatedAsyncioTestCase):
    async def test_h264_normalizer_passes_browser_compatible_source_unchanged(self):
        frame = VideoFrame("h264", 1, 0, 1000, b"h264-packet", True)

        class Source:
            async def read(self, *, timeout=15):
                return frame

        output = H264Video(Source())
        self.assertIs(await output.read(timeout=1), frame)

    async def test_active_viewers_share_reconnect_after_camera_transport_lost(self):
        sessions = []
        closed = []
        disconnect = asyncio.Event()
        keep_open = asyncio.Event()

        @asynccontextmanager
        async def open_mock():
            number = len(sessions) + 1
            sessions.append(number)

            class Video:
                sent = False

                async def read(self, *, timeout=15):
                    if not self.sent:
                        self.sent = True
                        # The new camera session restarts its timestamp clock.
                        return VideoFrame("h264", number, 0, 1000 if number == 1 else 0, b"", True)
                    if number == 1:
                        await disconnect.wait()
                        raise ConnectionResetError
                    await keep_open.wait()

            try:
                yield Video()
            finally:
                closed.append(number)

        source = LocalMediaSource(open_mock)
        async with asyncio.timeout(3), source._subscribe() as first, source._subscribe() as second:
            initial = await first.get()
            self.assertEqual(await second.get(), initial)
            disconnect.set()
            resumed = await first.get()
            self.assertEqual(await second.get(), resumed)
            self.assertEqual(resumed.sequence, 2)
            self.assertTrue(resumed.random_access)
            self.assertGreater(resumed.timestamp, initial.timestamp)
            self.assertEqual(sessions, [1, 2])
            self.assertEqual(closed, [1])
        self.assertEqual(closed, [1, 2])

    async def test_talkback_session_is_invalidated_and_can_restart_after_reconnect(self):
        videos = []
        disconnect = asyncio.Event()
        keep_open = asyncio.Event()
        sessions = {}

        class Video:
            def __init__(self, number):
                self.number = number
                self.sent = False
                self.started = 0
                self.stopped = 0
                self.frames = []

            async def start_speaker(self):
                self.started += 1

            async def write_speaker_audio(self, payload):
                self.frames.append(payload)

            async def stop_speaker(self):
                self.stopped += 1

            async def read(self, *, timeout=15):
                if not self.sent:
                    self.sent = True
                    return VideoFrame("h264", self.number, 0, 1000 if self.number == 1 else 0, b"", True)
                if self.number == 1:
                    await disconnect.wait()
                    raise ConnectionResetError
                await keep_open.wait()

        @asynccontextmanager
        async def open_mock():
            video = Video(len(videos) + 1)
            videos.append(video)
            try:
                yield video
            finally:
                pass

        source = LocalMediaSource(open_mock, audio=True, talkback=True)
        connection = SimpleNamespace(subscriptions={})
        sessions_store = sessions
        async with asyncio.timeout(3), source._subscribe() as queue:
            await queue.get()
            feed = source._feed
            old_token = await source.start_talkback()
            sessions_store[old_token] = (source, connection)
            connection.subscriptions[old_token] = lambda: None
            def invalidate(token):
                active = sessions_store.pop(token, None)
                if active is not None:
                    active[1].subscriptions.pop(token, None)
            feed.talkback_invalidate = invalidate

            disconnect.set()
            await queue.get()
            self.assertEqual(videos[0].stopped, 1)
            self.assertIsNone(feed.talkback_token)
            self.assertNotIn(old_token, sessions_store)
            self.assertNotIn(old_token, connection.subscriptions)
            with self.assertRaisesRegex(Exception, "no longer active"):
                await source.send_talkback(old_token, b"stale-frame")

            new_token = await source.start_talkback()
            self.assertNotEqual(new_token, old_token)
            await source.send_talkback(new_token, b"fresh-frame")
            self.assertEqual(videos[1].started, 1)
            self.assertEqual(videos[1].frames, [b"fresh-frame"])
            await source.stop_talkback(new_token)
            keep_open.set()

    async def test_two_http_players_share_video_when_enabled_audio_is_absent(self):
        picture = await asyncio.to_thread(
            ffmpeg, "-f", "lavfi", "-i", "color=red:size=64x64", "-frames:v", "1",
            "-c:v", "libx264", "-tune", "zerolatency", "-f", "h264", "pipe:1",
        )
        opens = []
        closed = asyncio.Event()

        class Video:
            index = 0

            async def read(self, *, timeout=15):
                await asyncio.sleep(0.03)
                self.index += 1
                return VideoFrame("h264", self.index, 0, self.index * 30, picture, True)

            read_media = read

        @asynccontextmanager
        async def open_mock():
            opens.append(1)
            try:
                yield Video()
            finally:
                closed.set()

        source = LocalMediaSource(open_mock, audio=True)
        await source.start()
        try:
            async with asyncio.timeout(4), ClientSession() as client:
                async with client.get(source.url) as first, client.get(source.url) as second:
                    self.assertEqual((first.status, second.status), (200, 200))
                    self.assertEqual((await first.content.readexactly(1), await second.content.readexactly(1)),
                                     (b"\x47", b"\x47"))
                    self.assertEqual(opens, [1])
                await closed.wait()
        finally:
            await source.close()

    async def test_c201_preserves_output_size_and_timestamp_order(self):
        packets = []
        for index, (size, timestamp) in enumerate((("64x64", 1000), ("96x80", 995)), 1):
            payload = await asyncio.to_thread(
                ffmpeg, "-f", "lavfi", "-i", f"color=red:size={size}", "-frames:v", "1",
                "-c:v", "libx265", "-tune", "zerolatency",
                "-x265-params", "pools=none:frame-threads=1:log-level=error", "-f", "hevc", "pipe:1",
            )
            packets.append(VideoFrame("h265", index, 0, timestamp, payload, True))

        class Source:
            async def read(self, *, timeout=15):
                return packets.pop(0)

        output = H264Video(Source())
        decoder = av.CodecContext.create("h264", "r")
        first, second = await output.read(timeout=2), await output.read(timeout=2)
        self.assertGreater(second.timestamp, first.timestamp)
        decoded = decoder.decode(av.Packet(first.payload)) + decoder.decode(av.Packet(second.payload))
        self.assertEqual([(frame.width, frame.height) for frame in decoded], [(64, 64), (64, 64)])

    async def test_config_flow_produces_native_ha_hls_and_preserves_identity(self):
        picture = await asyncio.to_thread(
            ffmpeg, "-f", "lavfi", "-i", "color=red:size=64x64", "-frames:v", "1",
            "-c:v", "libx265", "-tune", "zerolatency",
            "-x265-params", "pools=none:frame-threads=1:log-level=error", "-f", "hevc", "pipe:1",
        )
        opus = await asyncio.to_thread(
            ffmpeg, "-f", "lavfi", "-i", "sine=frequency=600:sample_rate=48000", "-t", "0.4", "-ac", "2",
            "-c:a", "libopus", "-frame_duration", "40", "-f", "ogg", "pipe:1",
        )
        with av.open(io.BytesIO(opus)) as ogg:
            microphone_packets = [bytes(packet) for packet in ogg.demux(audio=0) if packet.size]
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        starts = []
        closes = []
        moves = []
        audio_starts = []
        routes_used = []

        class FakeVideo:
            sequence = 0
            audio_due = False

            async def step(self, operation):
                moves.append(operation)

            async def read(self, *, timeout=15):
                await asyncio.sleep(0.05)
                self.sequence += 1
                return VideoFrame("h265", self.sequence, 0, self.sequence * 40, picture, True)

            async def read_media(self, *, timeout=15):
                self.audio_due = not self.audio_due
                if self.audio_due:
                    index = (self.sequence - 1) % len(microphone_packets)
                    return AudioPacket("opus", 48000, self.sequence * 40, microphone_packets[index])
                return await self.read(timeout=timeout)

        def fake_opener(_hass, config, _account, control, *, quality, entry, audio):
            @asynccontextmanager
            async def fake_open():
                starts.append((config["model"], quality))
                routes_used.append((str(config["camera_ip"]), str(config["source_ip"]), str(config["camera_lan"])))
                audio_starts.append(audio)
                video = FakeVideo()
                control.set_video(video)
                try:
                    yield H264Video(video)
                finally:
                    control.set_video(None)
                    closes.append(1)
            return fake_open

        with tempfile.TemporaryDirectory() as temporary, patch.object(camera, "_video_opener", side_effect=fake_opener):
            config = Path(temporary)
            shutil.copytree(Path(camera.__file__).parent, config / "custom_components/xiaomi_direct_pilot",
                            ignore=shutil.ignore_patterns("__pycache__"))
            (config / "configuration.yaml").write_text(f"""
http:
  server_host: 127.0.0.1
  server_port: {port}
stream:
  segment_duration: 2
  part_duration: 0.5
""", encoding="utf-8")
            root_logger = logging.getLogger()
            previous_handlers = set(root_logger.handlers)
            hass = None
            try:
                hass = await async_setup_hass(RuntimeConfig(str(config), skip_pip=True))
                self.assertIsNotNone(hass)
                await hass.async_start()
                await hass.async_block_till_done()
                flow = hass.config_entries.flow
                form = await flow.async_init("xiaomi_direct_pilot", context={"source": "user"})
                self.assertEqual(form["step_id"], "user")
                camera_inventory = [
                    {"model": "xiaomi.camera.c201", "did": "mock-c201", "name": "Xiaomi C201",
                     "localip": "192.168.99.2"},
                    {"model": "chuangmi.camera.079ae2", "did": "mock-c701", "name": "Xiaomi C701",
                     "localip": "192.168.99.5"},
                    {"model": "chuangmi.camera.039a04", "did": "mock-c400", "name": "Other camera",
                     "localip": "192.168.99.4"},
                    {"model": "test.camera.unknown", "did": "mock-unknown", "name": "Unknown camera"},
                ]
                account = SimpleNamespace(
                    user_id="mock-user", pass_token="mock-pass",
                    devices=AsyncMock(return_value={"list": camera_inventory}),
                )
                route_state = {"available": True, "source_ip": "192.168.99.3"}

                async def resolve(_hass, inventory):
                    return [
                        {**data, "source_ip": route_state["source_ip"], "camera_lan": "192.168.99.0/24"}
                        if data["camera_ip"] and route_state["available"] else data
                        for data in inventory.values()
                    ]

                with (
                    patch.object(config_flow.XiaomiAccount, "login", AsyncMock(return_value=account)),
                    patch.object(config_flow, "resolve_cameras", resolve),
                    patch.object(xiaomi_direct_pilot, "create_account", return_value=account),
                    patch.object(xiaomi_direct_pilot, "resolve_cameras", resolve),
                ):
                    form = await flow.async_configure(form["flow_id"], {
                        "region": "de", "method": "password",
                        "username": "mock@example.invalid", "password": "mock-password",
                    })
                    self.assertEqual(form["type"], "create_entry")
                    entry = form["result"]
                    self.assertNotIn("password", entry.data)
                    self.assertEqual(entry.title, "Xiaomi Camera Direct")
                    await hass.async_block_till_done()
                c400_id = "camera.other_camera_direct"
                self.assertEqual(hass.states.get("camera.unknown_camera_direct").state, "unavailable")
                self.assertEqual(hass.states.get(c400_id).attributes["requested_video_profile"], 3)
                self.assertEqual(starts, [])
                hls = await async_request_stream(hass, "camera.xiaomi_c201_direct", "hls")
                url = f"http://127.0.0.1:{port}{hls}"
                async with ClientSession() as client:
                    async with client.get(url, timeout=15) as response:
                        self.assertEqual(response.status, 200)
                        playlist = await response.text()
                        self.assertIn("#EXTM3U", playlist)
                        self.assertIn("avc1", playlist)
                        self.assertNotIn("mp4a", playlist)
                pixels = await asyncio.to_thread(
                    ffmpeg, "-i", url, "-frames:v", "1", "-pix_fmt", "yuv420p", "-f", "rawvideo", "pipe:1",
                )
                self.assertEqual(len(pixels), 64 * 64 * 3 // 2)
                entity = get_camera_from_entity_id(hass, "camera.xiaomi_c201_direct")
                image = await entity.stream.async_get_image()
                self.assertTrue(image and image.startswith(b"\xff\xd8"))
                await hass.services.async_call(
                    "button", "press", blocking=True,
                    target={"entity_id": "button.xiaomi_c201_direct_ptz_left"},
                )
                self.assertEqual(moves, [1])
                self.assertEqual(starts, [("xiaomi.camera.c201", 0)])
                self.assertEqual(audio_starts, [False])
                registry = entity_registry.async_get(hass)
                camera_id = "camera.xiaomi_c201_direct"
                device_id = registry.async_get(camera_id).device_id
                self.assertIsNotNone(device_id)
                self.assertEqual(registry.async_get("button.xiaomi_c201_direct_ptz_left").device_id, device_id)

                async def submit_reauth(user_id):
                    pending = await flow.async_init(entry.domain, context={
                        "source": "reauth", "entry_id": entry.entry_id,
                    }, data=entry.data)
                    self.assertEqual(pending["step_id"], "reauth_confirm")
                    renewed = SimpleNamespace(user_id=user_id, pass_token="renewed", devices=account.devices)
                    with patch.object(config_flow.XiaomiAccount, "login", AsyncMock(return_value=renewed)):
                        return await flow.async_configure(pending["flow_id"], {
                            "method": "password", "username": "mock@example.invalid", "password": "mock-password",
                        })

                with patch.object(xiaomi_direct_pilot, "create_account", return_value=SimpleNamespace(
                    devices=AsyncMock(return_value={}),
                )):
                    self.assertEqual((await submit_reauth("wrong-user"))["reason"], "wrong_account")
                    self.assertEqual(entry.data["account_pass_token"], "mock-pass")
                    self.assertEqual((await submit_reauth("mock-user"))["reason"], "reauth_successful")
                    await hass.async_block_till_done()
                    self.assertEqual(entry.data["account_pass_token"], "renewed")

                    options = hass.config_entries.options
                    form = await options.async_init(entry.entry_id)
                    self.assertEqual(form["step_id"], "init")
                    with self.assertRaises(InvalidData):
                        await options.async_configure(form["flow_id"], {
                            "camera_ids": ["mock-c201"], "c400_low_resolution": ["mock-c201"],
                        })
                    result = await options.async_configure(form["flow_id"], {
                        "camera_ids": ["mock-c201", "mock-c400"], "c400_low_resolution": ["mock-c400"],
                    })
                    self.assertEqual(result["type"], "create_entry")
                    await hass.async_block_till_done()
                    self.assertEqual(hass.states.get(c400_id).attributes["requested_video_profile"], 2)
                    self.assertEqual(starts, [("xiaomi.camera.c201", 0)])
                    c400 = get_camera_from_entity_id(hass, c400_id)
                    async with ClientSession() as client, client.get(await c400.stream_source(), timeout=5) as stream:
                        self.assertEqual(await stream.content.readexactly(1), b"\x47")
                    self.assertEqual(starts, [("xiaomi.camera.c201", 0), ("chuangmi.camera.039a04", 2)])

                    form = await options.async_init(entry.entry_id)
                    result = await options.async_configure(form["flow_id"], {
                        "camera_ids": ["mock-c201", "mock-c400"], "c400_low_resolution": [],
                    })
                    self.assertEqual(result["type"], "create_entry")
                    await hass.async_block_till_done()
                    self.assertEqual(hass.states.get(c400_id).attributes["requested_video_profile"], 3)

                    form = await options.async_init(entry.entry_id)
                    result = await options.async_configure(form["flow_id"], {
                        "camera_ids": ["mock-c201", "mock-c701"],
                        "c201_audio": ["mock-c201"], "c701_audio": ["mock-c701"],
                    })
                    self.assertEqual(result["type"], "create_entry")
                    await hass.async_block_till_done()
                with self.assertRaisesRegex(HomeAssistantError, "Camera not found"):
                    get_camera_from_entity_id(hass, c400_id)
                self.assertIsNotNone(get_camera_from_entity_id(hass, camera_id))
                self.assertEqual(registry.async_get(camera_id).device_id, device_id)
                self.assertEqual(len(starts), 2)
                hls = await async_request_stream(hass, camera_id, "hls")
                samples = await asyncio.to_thread(
                    ffmpeg, "-i", f"http://127.0.0.1:{port}{hls}", "-map", "0:a:0",
                    "-t", "0.2", "-acodec", "pcm_s16le", "-f", "s16le", "pipe:1",
                )
                self.assertEqual(len(samples), 6400)
                self.assertTrue(any(samples))
                self.assertEqual(audio_starts, [False, False, True])
                self.assertEqual(starts[-1], ("xiaomi.camera.c201", 2))

                c701_id = "camera.xiaomi_c701_direct"
                c701 = get_camera_from_entity_id(hass, c701_id)
                self.assertTrue(hass.states.get(c701_id).attributes["audio_enabled"])
                self.assertTrue(hass.states.get(c701_id).attributes["talkback_enabled"])
                self.assertEqual(hass.states.get(c701_id).attributes["requested_video_profile"], 0)
                c701_hls = await async_request_stream(hass, c701_id, "hls")
                c701_samples = await asyncio.to_thread(
                    ffmpeg, "-i", f"http://127.0.0.1:{port}{c701_hls}", "-map", "0:a:0",
                    "-t", "0.2", "-acodec", "pcm_s16le", "-f", "s16le", "pipe:1",
                )
                self.assertEqual(len(c701_samples), 6400)
                self.assertTrue(any(c701_samples))
                self.assertEqual(audio_starts, [False, False, True, True])
                self.assertEqual(starts[-1], ("chuangmi.camera.079ae2", 0))

                route_state["available"] = False
                with (
                    patch.object(xiaomi_direct_pilot, "create_account", return_value=account),
                    patch.object(xiaomi_direct_pilot, "resolve_cameras", resolve),
                ):
                    self.assertTrue(await hass.config_entries.async_reload(entry.entry_id))
                    await hass.async_block_till_done()
                    current = {item["device_id"]: item for item in entry.data["cameras"]}["mock-c201"]
                    self.assertNotIn("source_ip", current)
                    self.assertNotIn("camera_lan", current)
                    unavailable = get_camera_from_entity_id(hass, camera_id)
                    self.assertFalse(unavailable.available)
                    self.assertIsNone(await unavailable.stream_source())
                    self.assertEqual(hass.states.get(camera_id).state, "unavailable")
                    self.assertEqual(registry.async_get(camera_id).entity_id, camera_id)
                    self.assertEqual(registry.async_get(camera_id).device_id, device_id)

                    route_state.update(available=True, source_ip="192.168.99.8")
                    self.assertTrue(await hass.config_entries.async_reload(entry.entry_id))
                    await hass.async_block_till_done()
                    restored = get_camera_from_entity_id(hass, camera_id)
                    self.assertTrue(restored.available)
                    self.assertIsNotNone(await restored.stream_source())
                    current = {item["device_id"]: item for item in entry.data["cameras"]}["mock-c201"]
                    self.assertEqual(current["source_ip"], "192.168.99.8")
                    self.assertEqual(registry.async_get(camera_id).entity_id, camera_id)
                    self.assertEqual(registry.async_get(camera_id).device_id, device_id)
                    async with ClientSession() as client, client.get(
                        await restored.stream_source(), timeout=5,
                    ) as stream:
                        self.assertEqual(await stream.content.readexactly(1), b"\x47")
                    self.assertEqual(routes_used[-1][1], "192.168.99.8")
            finally:
                try:
                    if hass is not None:
                        await hass.async_stop(force=True)
                finally:
                    for handler in set(root_logger.handlers) - previous_handlers:
                        root_logger.removeHandler(handler)
                        handler.close()
        self.assertEqual(len(closes), 5)
        self.assertTrue(all(count == 1 for count in closes))

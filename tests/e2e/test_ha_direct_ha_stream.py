"""Boot a disposable HA instance and decode its HLS from a simulated C201."""

import asyncio
from contextlib import asynccontextmanager
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
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers import entity_registry
from homeassistant.runner import RuntimeConfig

from custom_components import xiaomi_direct_pilot
from custom_components.xiaomi_direct_pilot import camera, config_flow
from custom_components.xiaomi_direct_pilot.xiaomi_direct.h264_video import H264Video
from custom_components.xiaomi_direct_pilot.xiaomi_direct.ha_media import LocalMediaSource
from custom_components.xiaomi_direct_pilot.xiaomi_direct.miss_media import VideoFrame
from test_ha_direct_media_decode import ffmpeg


class DirectHaStreamTest(unittest.IsolatedAsyncioTestCase):
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

    async def test_two_http_players_share_the_same_camera_session(self):
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

        @asynccontextmanager
        async def open_mock():
            opens.append(1)
            try:
                yield Video()
            finally:
                closed.set()

        source = LocalMediaSource(open_mock)
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
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        starts = []
        closes = []
        moves = []

        class FakeVideo:
            sequence = 0

            async def step(self, operation):
                moves.append(operation)

            async def read(self, *, timeout=15):
                await asyncio.sleep(0.05)
                self.sequence += 1
                return VideoFrame("h265", self.sequence, 0, self.sequence * 50, picture, True)

        def fake_opener(_hass, _config, _account, control, *, entry):
            @asynccontextmanager
            async def fake_open():
                starts.append(1)
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
                account = SimpleNamespace(
                    user_id="mock-user", pass_token="mock-pass",
                    devices=AsyncMock(return_value={"list": [
                        {"model": "xiaomi.camera.c201", "did": "mock-c201", "name": "Xiaomi C201",
                         "localip": "192.168.99.2"},
                        {"model": "chuangmi.camera.039a04", "did": "mock-c400", "name": "Other camera"},
                    ]}),
                )
                async def resolve(_hass, inventory):
                    return [
                        {**data, "source_ip": "192.168.99.3", "camera_lan": "192.168.99.0/24"}
                        if data["device_id"] == "mock-c201" else data
                        for data in inventory.values()
                    ]

                with (
                    patch.object(config_flow.XiaomiAccount, "login", AsyncMock(return_value=account)),
                    patch.object(config_flow, "resolve_cameras", resolve),
                    patch.object(xiaomi_direct_pilot, "create_account", return_value=SimpleNamespace(
                        devices=AsyncMock(return_value={}),
                    )),
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
                self.assertEqual(hass.states.get("camera.other_camera_direct").state, "unavailable")
                self.assertEqual(starts, [])
                hls = await async_request_stream(hass, "camera.xiaomi_c201_direct", "hls")
                url = f"http://127.0.0.1:{port}{hls}"
                async with ClientSession() as client:
                    async with client.get(url, timeout=15) as response:
                        self.assertEqual(response.status, 200)
                        playlist = await response.text()
                        self.assertIn("#EXTM3U", playlist)
                        self.assertIn("avc1", playlist)
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
                self.assertEqual(starts, [1])
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
                    result = await options.async_configure(form["flow_id"], {"camera_ids": ["mock-c201"]})
                    self.assertEqual(result["type"], "create_entry")
                    await hass.async_block_till_done()
                with self.assertRaisesRegex(HomeAssistantError, "Camera not found"):
                    get_camera_from_entity_id(hass, "camera.other_camera_direct")
                self.assertIsNotNone(get_camera_from_entity_id(hass, camera_id))
                self.assertEqual(registry.async_get(camera_id).device_id, device_id)
                self.assertEqual(starts, [1])
            finally:
                try:
                    if hass is not None:
                        await hass.async_stop(force=True)
                finally:
                    for handler in set(root_logger.handlers) - previous_handlers:
                        root_logger.removeHandler(handler)
                        handler.close()
        self.assertEqual(closes, [1])

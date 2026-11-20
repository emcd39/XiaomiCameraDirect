"""Normalize HEVC camera video to browser-compatible H.264."""

import asyncio
from collections import deque
from fractions import Fraction

import av

from .miss_media import AudioPacket, VideoFrame


_CLOCK = Fraction(1, 1000)


class H264Video:
    """Transcode HEVC on demand and pass existing H.264 through unchanged."""

    def __init__(self, video):
        self._video = video
        self._decoder = None
        self._encoder = None
        self._pending = deque()
        self._origin = None
        self._last_pts = -1
        self._sequence = 0

    def _encode(self, source):
        if source.codec == "h264":
            return (source,)
        if self._decoder is None:
            self._decoder = av.CodecContext.create("hevc", "r")
            # Frame threading can buffer a substantial portion of a short GOP.
            self._decoder.thread_count = 1
            self._origin = source.timestamp
        packet = av.Packet(source.payload)
        packet.pts = packet.dts = source.timestamp - self._origin
        packet.time_base = _CLOCK
        try:
            pictures = self._decoder.decode(packet)
        except av.InvalidDataError:
            return ()
        frames = []
        for picture in pictures:
            if picture.is_corrupt:
                continue
            if self._encoder is None:
                encoder = av.CodecContext.create("libx264", "w")
                encoder.width = picture.width
                encoder.height = picture.height
                encoder.pix_fmt = "yuv420p"
                encoder.framerate = Fraction(20)
                encoder.time_base = _CLOCK
                encoder.options = {
                    "preset": "ultrafast", "tune": "zerolatency", "crf": "22",
                    "x264-params": "keyint=20:min-keyint=20:scenecut=0:repeat-headers=1",
                }
                encoder.open()
                self._encoder = encoder
            encoder = self._encoder
            timestamp = picture.pts if picture.pts is not None else self._last_pts + 50
            # Force monotonic presentation timestamps on duplicated or reordered input.
            timestamp = max(self._last_pts + 1, timestamp)
            self._last_pts = timestamp
            picture = picture.reformat(width=encoder.width, height=encoder.height, format=encoder.pix_fmt)
            picture.pts = timestamp
            picture.time_base = _CLOCK
            for encoded in encoder.encode(picture):
                self._sequence += 1
                frames.append(VideoFrame(
                    "h264", self._sequence, 0,
                    self._origin + (encoded.pts if encoded.pts is not None else timestamp),
                    bytes(encoded), encoded.is_keyframe,
                ))
        return frames

    async def read(self, *, timeout=15):
        return await self._read(self._video.read, timeout)

    async def read_media(self, *, timeout=15):
        return await self._read(self._video.read_media, timeout)

    async def start_speaker(self):
        await self._video.start_speaker()

    async def write_speaker_audio(self, payload):
        await self._video.write_speaker_audio(payload)

    async def stop_speaker(self):
        await self._video.stop_speaker()

    async def _read(self, read, timeout):
        async with asyncio.timeout(timeout):
            while not self._pending:
                frame = await read(timeout=timeout)
                if isinstance(frame, AudioPacket):
                    return frame
                self._pending.extend(await asyncio.to_thread(self._encode, frame))
        return self._pending.popleft()

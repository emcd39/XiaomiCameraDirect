"""Present encoded MISS pictures to the HA stream worker as local MPEG-TS."""

import asyncio
from collections.abc import Callable
from contextlib import AsyncExitStack, asynccontextmanager
from dataclasses import dataclass, field, replace
from fractions import Fraction
import io
import logging
import secrets
from time import monotonic

from aiohttp import web
import av

from .aac_audio import AACAudio, supports_audio
from .account import AccountError
from .cs2_tcp import CS2Error
from .miss_media import AudioPacket, MediaFormatError, VideoFrame


_TIME_BASE = Fraction(1, 1000)
_LOGGER = logging.getLogger(__name__)
_END = object()
_TRANSPORT_ERRORS = (CS2Error, TimeoutError, OSError)
_CAMERA_ERRORS = (AccountError, MediaFormatError, av.FFmpegError, *_TRANSPORT_ERRORS)
_TALKBACK_DEADMAN_SECONDS = 0.8
_TALKBACK_MAX_SECONDS = 30


class TalkbackError(Exception):
    """The camera stream cannot accept a talkback packet."""


async def _first_keyframe(video):
    async with asyncio.timeout(7):
        while not (frame := await video.read()).random_access:
            pass
    return frame


@asynccontextmanager
async def _ready_video(open_video, attempts, label):
    deadline = asyncio.get_running_loop().time() + 22
    for attempt in range(attempts):
        async with AsyncExitStack() as stack:
            started = monotonic()
            stage = "open_session"
            try:
                async with asyncio.timeout_at(deadline):
                    video = await stack.enter_async_context(open_video())
                    opened = monotonic()
                    stage = "first_keyframe"
                    frame = await _first_keyframe(video)
            except (CS2Error, TimeoutError, ConnectionError):
                _LOGGER.debug("Direct camera %s attempt %d failed at %s", label, attempt + 1, stage)
                if attempt + 1 == attempts or asyncio.get_running_loop().time() >= deadline:
                    raise
                continue
            _LOGGER.debug("Direct camera %s session %.2fs, keyframe %.2fs",
                          label, opened - started, monotonic() - opened)
            yield video, frame
            return


async def _next_frame(queue):
    item = await queue.get()
    if item is _END:
        return None
    return item


async def _audio_prelude(queue):
    """Give the audio track a short opportunity to appear before muxing video."""
    pending = []
    try:
        async with asyncio.timeout(0.25):
            while len(pending) < 12:
                packet = await _next_frame(queue)
                pending.append(packet)
                if packet is None:
                    break
                if isinstance(packet, AudioPacket) and supports_audio(packet):
                    return pending, packet
    except TimeoutError:
        pass
    return pending, None


async def _stream_response(request, queue, *, audio_enabled):
    response = web.StreamResponse(headers={
        "Content-Type": "video/mp2t",
        "Cache-Control": "no-store",
    })
    frame = await _next_frame(queue)
    while frame is not None and (not isinstance(frame, VideoFrame) or not frame.random_access):
        frame = await _next_frame(queue)
    if frame is None:
        raise web.HTTPServiceUnavailable(text="Xiaomi direct video unavailable")
    pending, audio_format = await _audio_prelude(queue) if audio_enabled else ([], None)
    following = iter(pending)
    codec = frame.codec
    origin = frame.timestamp
    previous = -1
    await response.prepare(request)

    try:
        with io.BytesIO() as buffer, av.open(
            buffer, "w", format="mpegts",
            options={"flush_packets": "1", "max_interleave_delta": "250000"},
        ) as output:
            stream = output.add_mux_stream("hevc" if codec == "h265" else "h264")
            audio = None
            if audio_format is not None:
                try:
                    audio = AACAudio(output, audio_format, origin)
                except (av.FFmpegError, ValueError):
                    _LOGGER.debug("AAC encoder unavailable for Xiaomi direct stream")
            while frame is not None:
                if isinstance(frame, AudioPacket):
                    if audio is not None:
                        try:
                            for encoded in audio.encode(frame):
                                output.mux(encoded)
                        except (av.FFmpegError, ValueError):
                            audio = None
                else:
                    timestamp = max(previous + 1, frame.timestamp - origin)
                    if frame.codec != codec or timestamp - previous > 30_000:
                        break
                    packet = av.Packet(frame.payload)
                    packet.stream = stream
                    packet.time_base = _TIME_BASE
                    packet.pts = packet.dts = timestamp
                    packet.is_keyframe = frame.random_access
                    output.mux(packet)
                    previous = timestamp
                if chunk := buffer.getvalue():
                    async with asyncio.timeout(5):
                        await response.write(chunk)
                    buffer.seek(0)
                    buffer.truncate()
                frame = next(following, _END)
                if frame is _END:
                    frame = await _next_frame(queue)
    except (av.FFmpegError, TimeoutError, OSError):
        pass
    return response


@dataclass
class _Feed:
    subscribers: set[asyncio.Queue] = field(default_factory=set)
    task: asyncio.Task | None = None
    video: object | None = None
    talkback_token: str | None = None
    talkback_started: float | None = None
    talkback_last_packet: float | None = None
    talkback_task: asyncio.Task | None = None
    talkback_invalidate: Callable | None = None

    def publish(self, frame):
        for queue in tuple(self.subscribers):
            if queue.full():
                self.subscribers.remove(queue)
                _end_queue(queue)
            else:
                queue.put_nowait(frame)


def _end_queue(queue):
    while not queue.empty():
        queue.get_nowait()
    queue.put_nowait(_END)


class LocalMediaSource:
    """Own a loopback HTTP listener. The URL is only handed to HA's worker."""

    def __init__(self, open_video: Callable, *, label="C201", attempts=1, audio=False, talkback=False):
        self._open_video = open_video
        self._label = label
        self._attempts = attempts
        self._audio = audio
        self._talkback = talkback
        self._runner = None
        self._feed = None
        self._feed_lock = asyncio.Lock()
        self._talkback_lock = asyncio.Lock()
        self.url = None

    async def start(self):
        path = "/" + secrets.token_urlsafe(32)
        app = web.Application()
        app.router.add_get(path, self._handle, allow_head=False)
        runner = web.AppRunner(app, access_log=None, handler_cancellation=True, shutdown_timeout=1)
        await runner.setup()
        try:
            await web.TCPSite(runner, "127.0.0.1", 0).start()
        except BaseException:
            await runner.cleanup()
            raise
        self._runner = runner
        self.url = f"http://127.0.0.1:{runner.addresses[0][1]}{path}"

    async def close(self):
        if self._runner is not None:
            runner = self._runner
            self._runner = self.url = None
            await runner.cleanup()

    async def _handle(self, request):
        async with self._subscribe() as queue:
            return await _stream_response(request, queue, audio_enabled=self._audio)

    async def start_talkback(self):
        """Open the camera speaker only while its enabled live stream is active."""
        if not self._talkback:
            raise TalkbackError("Talkback is supported only for an enabled C701 audio stream")
        async with self._talkback_lock:
            feed = self._feed
            if feed is None or not feed.subscribers or feed.video is None:
                raise TalkbackError("Open the C701 live video before starting talkback")
            if feed.talkback_token is not None:
                raise TalkbackError("C701 talkback is already in use")
            video = feed.video
            try:
                await video.start_speaker()
            except (CS2Error, TimeoutError, OSError) as error:
                raise TalkbackError("Could not start the C701 speaker") from error
            if self._feed is not feed or feed.video is not video or not feed.subscribers:
                try:
                    await video.stop_speaker()
                except (CS2Error, TimeoutError, OSError):
                    pass
                raise TalkbackError("The C701 live stream stopped while starting talkback")
            token = secrets.token_urlsafe(24)
            now = monotonic()
            feed.talkback_token = token
            feed.talkback_started = feed.talkback_last_packet = now
            feed.talkback_task = asyncio.create_task(self._watch_talkback(feed, token))
            return token

    async def send_talkback(self, token, payload):
        if not isinstance(payload, bytes) or not 0 < len(payload) <= 4096:
            raise TalkbackError("Invalid Opus audio frame")
        async with self._talkback_lock:
            feed = self._feed
            if feed is None or feed.talkback_token != token or feed.video is None:
                raise TalkbackError("C701 talkback is no longer active")
            if monotonic() - (feed.talkback_last_packet or 0) > _TALKBACK_DEADMAN_SECONDS:
                raise TalkbackError("C701 talkback timed out; press and hold again")
            try:
                await feed.video.write_speaker_audio(payload)
            except (CS2Error, TimeoutError, OSError) as error:
                raise TalkbackError("Could not send audio to the C701 speaker") from error
            feed.talkback_last_packet = monotonic()

    async def stop_talkback(self, token):
        watchdog = None
        async with self._talkback_lock:
            feed = self._feed
            if feed is None or feed.talkback_token != token:
                return
            feed.talkback_token = None
            invalidate = feed.talkback_invalidate
            feed.talkback_invalidate = None
            watchdog = feed.talkback_task
            feed.talkback_task = None
            if watchdog is not None and watchdog is not asyncio.current_task():
                watchdog.cancel()
            video = feed.video
            if video is not None:
                try:
                    await video.stop_speaker()
                except (CS2Error, TimeoutError, OSError):
                    _LOGGER.debug("C701 speaker stop was not confirmed")
            if invalidate is not None:
                invalidate(token)
        if watchdog is not None and watchdog is not asyncio.current_task():
            await asyncio.gather(watchdog, return_exceptions=True)

    async def _watch_talkback(self, feed, token):
        try:
            while True:
                await asyncio.sleep(0.2)
                async with self._talkback_lock:
                    if feed.talkback_token != token:
                        return
                    now = monotonic()
                    expired = (
                        now - (feed.talkback_last_packet or now) > _TALKBACK_DEADMAN_SECONDS
                        or now - (feed.talkback_started or now) > _TALKBACK_MAX_SECONDS
                    )
                if expired:
                    await self.stop_talkback(token)
                    return
        except asyncio.CancelledError:
            raise

    @asynccontextmanager
    async def _subscribe(self):
        queue = asyncio.Queue(maxsize=16)
        async with self._feed_lock:
            feed = self._feed
            if feed is None or feed.task.done():
                feed = self._feed = _Feed()
                feed.task = asyncio.create_task(self._produce(feed))
            feed.subscribers.add(queue)
        try:
            yield queue
        finally:
            async with self._feed_lock:
                feed.subscribers.discard(queue)
                if not feed.subscribers and feed is self._feed:
                    self._feed = None
                    feed.task.cancel()
                    await asyncio.gather(feed.task, return_exceptions=True)

    async def _produce(self, feed):
        last_timestamp = None
        last_arrival = None
        try:
            for reconnect in (False, True):
                try:
                    attempts = 1 if reconnect else self._attempts
                    async with _ready_video(self._open_video, attempts, self._label) as (video, frame):
                        if feed.video is not None and feed.video is not video and feed.talkback_token is not None:
                            await self.stop_talkback(feed.talkback_token)
                        feed.video = video
                        offset = 0
                        if reconnect:
                            elapsed_ms = max(1, int((monotonic() - last_arrival) * 1000))
                            offset = last_timestamp + elapsed_ms - frame.timestamp
                        read = video.read_media if self._audio else video.read
                        loop = asyncio.get_running_loop()
                        video_deadline = loop.time() + 12
                        while feed.subscribers:
                            if offset:
                                frame = replace(frame, timestamp=frame.timestamp + offset)
                            feed.publish(frame)
                            if isinstance(frame, VideoFrame):
                                last_timestamp = max(last_timestamp, frame.timestamp) if last_timestamp is not None else frame.timestamp
                                last_arrival = monotonic()
                                video_deadline = loop.time() + 12
                            if not feed.subscribers:
                                return
                            async with asyncio.timeout_at(video_deadline):
                                frame = await read(timeout=12)
                        return
                except _TRANSPORT_ERRORS:
                    if feed.talkback_token is not None:
                        await self.stop_talkback(feed.talkback_token)
                    if reconnect or last_timestamp is None or not feed.subscribers:
                        raise
                    _LOGGER.debug("Direct camera %s reconnecting for active viewers", self._label)
                    await asyncio.sleep(0.5)
        except _CAMERA_ERRORS as error:
            reason = str(error) if isinstance(error, (AccountError, CS2Error)) else type(error).__name__
            _LOGGER.warning("Direct camera %s video stopped: %s", self._label, reason)
        finally:
            if feed.talkback_token is not None:
                await self.stop_talkback(feed.talkback_token)
            feed.video = None
            for queue in feed.subscribers:
                _end_queue(queue)

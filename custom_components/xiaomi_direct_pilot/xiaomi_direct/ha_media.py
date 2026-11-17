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

from .account import AccountError
from .cs2_tcp import CS2Error
from .miss_media import MediaFormatError


_TIME_BASE = Fraction(1, 1000)
_LOGGER = logging.getLogger(__name__)
_END = object()
_TRANSPORT_ERRORS = (CS2Error, TimeoutError, OSError)
_CAMERA_ERRORS = (AccountError, MediaFormatError, av.FFmpegError, *_TRANSPORT_ERRORS)


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


async def _stream_response(request, queue):
    response = web.StreamResponse(headers={
        "Content-Type": "video/mp2t",
        "Cache-Control": "no-store",
    })
    while (frame := await _next_frame(queue)) is not None and not frame.random_access:
        pass
    if frame is None:
        raise web.HTTPServiceUnavailable(text="Xiaomi direct video unavailable")
    codec = frame.codec
    origin = frame.timestamp
    previous = -1
    await response.prepare(request)

    try:
        with io.BytesIO() as buffer, av.open(
            buffer, "w", format="mpegts", options={"flush_packets": "1"},
        ) as output:
            stream = output.add_mux_stream("hevc" if codec == "h265" else "h264")
            while frame is not None and frame.codec == codec:
                timestamp = max(previous + 1, frame.timestamp - origin)
                if timestamp - previous > 30_000:
                    break
                packet = av.Packet(frame.payload)
                packet.stream = stream
                packet.time_base = _TIME_BASE
                packet.pts = packet.dts = timestamp
                packet.is_keyframe = frame.random_access
                output.mux(packet)
                if chunk := buffer.getvalue():
                    async with asyncio.timeout(5):
                        await response.write(chunk)
                    buffer.seek(0)
                    buffer.truncate()
                previous = timestamp
                frame = await _next_frame(queue)
    except (av.FFmpegError, TimeoutError, OSError):
        pass
    return response


@dataclass
class _Feed:
    subscribers: set[asyncio.Queue] = field(default_factory=set)
    task: asyncio.Task | None = None

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

    def __init__(self, open_video: Callable, *, label="C201", attempts=1):
        self._open_video = open_video
        self._label = label
        self._attempts = attempts
        self._runner = None
        self._feed = None
        self._feed_lock = asyncio.Lock()
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
            return await _stream_response(request, queue)

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
                        offset = 0
                        if reconnect:
                            elapsed_ms = max(1, int((monotonic() - last_arrival) * 1000))
                            offset = last_timestamp + elapsed_ms - frame.timestamp
                        while feed.subscribers:
                            if offset:
                                frame = replace(frame, timestamp=frame.timestamp + offset)
                            feed.publish(frame)
                            last_timestamp = max(last_timestamp, frame.timestamp) if last_timestamp is not None else frame.timestamp
                            last_arrival = monotonic()
                            if not feed.subscribers:
                                return
                            frame = await video.read(timeout=12)
                        return
                except _TRANSPORT_ERRORS:
                    if reconnect or last_timestamp is None or not feed.subscribers:
                        raise
                    _LOGGER.debug("Direct camera %s reconnecting for active viewers", self._label)
                    await asyncio.sleep(0.5)
        except _CAMERA_ERRORS as error:
            reason = str(error) if isinstance(error, (AccountError, CS2Error)) else type(error).__name__
            _LOGGER.warning("Direct camera %s video stopped: %s", self._label, reason)
        finally:
            for queue in feed.subscribers:
                _end_queue(queue)

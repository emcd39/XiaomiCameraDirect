"""Open a video-only C201 session with credentials issued by Xiaomi."""

import asyncio
from collections.abc import Awaitable, Callable
from contextlib import asynccontextmanager, suppress
from dataclasses import dataclass, field
import json
import secrets
from struct import Struct
import time

from cryptography.hazmat.primitives.ciphers import Cipher, algorithms
from nacl.bindings import crypto_box_beforenm, crypto_scalarmult_base
from nacl.exceptions import CryptoError
from nacl.public import PrivateKey

from .cs2_tcp import CS2Error, open_transport
from .miss_media import (
    AUDIO_CODECS, AudioPacket, MediaFormatError, VideoFrame, decode_audio_packet, decode_video_frame,
)


_COMMAND = Struct(">I")
_CODEC = Struct("<4xI")
_SPEAKER_HEADER = Struct("<IIIIQ8x")
_SPEAKER_CODEC_OPUS = 1032
_SPEAKER_CHANNEL = 3
_SPEAKER_START_DELAY = 1


@dataclass(frozen=True, slots=True)
class SignedSession:
    """Ephemeral credentials returned by the authorized account flow."""

    private_key: bytes = field(repr=False)
    camera_public_key: bytes = field(repr=False)
    signature: str = field(repr=False)


async def request_signed_session(
    account_request: Callable[[str, dict], Awaitable[dict]], device_id: str
) -> SignedSession:
    """Ask an already signed-in Xiaomi account for fresh CS2 credentials."""
    private = PrivateKey.generate()
    reply = await account_request("/v2/device/miss_get_vendor", {
        "app_pubkey": bytes(private.public_key).hex(),
        "did": device_id,
        "support_vendors": "TUTK_CS2_MTP",
    })
    vendor = reply.get("vendor") if isinstance(reply, dict) else None
    if not isinstance(vendor, dict) or vendor.get("vendor") != 4:
        raise CS2Error("Xiaomi account did not nominate CS2")
    signature = reply.get("sign")
    camera_public = reply.get("public_key")
    if not isinstance(signature, str) or not signature or not isinstance(camera_public, str) or len(camera_public) != 64:
        raise CS2Error("invalid signed MISS session response")
    try:
        camera_key = bytes.fromhex(camera_public)
    except ValueError:
        raise CS2Error("invalid signed MISS session response") from None
    if len(camera_key) != 32:
        raise CS2Error("invalid signed MISS session response")
    return SignedSession(bytes(private), camera_key, signature)


def _encrypt(payload, key):
    nonce = secrets.token_bytes(8)
    cipher = Cipher(algorithms.ChaCha20(key, bytes(8) + nonce), mode=None).encryptor()
    return nonce + cipher.update(payload) + cipher.finalize()


async def _start(transport, credentials, quality, audio):
    if len(credentials.camera_public_key) != 32 or len(credentials.private_key) != 32 or not credentials.signature:
        raise CS2Error("invalid signed MISS session credentials")
    try:
        public_key = crypto_scalarmult_base(credentials.private_key).hex()
        key = crypto_box_beforenm(credentials.camera_public_key, credentials.private_key)
    except (CryptoError, ValueError):
        raise CS2Error("MISS key agreement failed") from None
    login = json.dumps({
        "public_key": public_key,
        "sign": credentials.signature,
        "uuid": "",
        "support_encrypt": 0,
    }, separators=(",", ":")).encode()
    await transport.send_command(0x100, login)
    while True:
        channel, message = await transport.receive()
        if channel == 0:
            break
    try:
        reply = json.loads(message[_COMMAND.size:])
    except (ValueError, UnicodeError):
        raise CS2Error("invalid MISS authentication response") from None
    if not isinstance(reply, dict) or reply.get("result") != "success":
        raise CS2Error("MISS authentication rejected")
    start = _COMMAND.pack(0x102) + json.dumps(
        {"videoquality": quality, "enableaudio": int(audio)}, separators=(",", ":"),
    ).encode()
    await transport.send_command(0x1001, _encrypt(start, key))
    return key


class C201Video:
    """Authenticated MISS media session, including optional audio/backchannel."""

    def __init__(self, transport, key, *, audio=False):
        self._transport = transport
        self._key = key
        self._audio = audio
        self._speaker_started = False

    async def step(self, operation):
        if operation not in (1, 2, 3, 4):
            raise ValueError("invalid PTZ operation")
        command = _COMMAND.pack(0x112) + json.dumps({"operation": operation}, separators=(",", ":")).encode()
        await self._transport.send_command(0x1001, _encrypt(command, self._key))

    async def start_speaker(self):
        if not self._audio:
            raise CS2Error("C701 talkback requires microphone audio to be enabled")
        if self._speaker_started:
            return
        await self._transport.send_command(0x1001, _encrypt(_COMMAND.pack(0x106), self._key))
        self._speaker_started = True
        try:
            await asyncio.sleep(_SPEAKER_START_DELAY)
        except asyncio.CancelledError:
            with suppress(CS2Error, OSError, TimeoutError):
                await self.stop_speaker()
            raise

    async def write_speaker_audio(self, payload: bytes):
        if not self._speaker_started:
            raise CS2Error("C701 talkback is not active")
        if not isinstance(payload, bytes) or not 0 < len(payload) <= 4096:
            raise CS2Error("invalid C701 Opus packet")
        encrypted = _encrypt(payload, self._key)
        header = _SPEAKER_HEADER.pack(
            len(encrypted), _SPEAKER_CODEC_OPUS, 0, 0, int(time.time() * 1000),
        )
        await self._transport.send_channel(_SPEAKER_CHANNEL, header + encrypted)

    async def stop_speaker(self):
        if not self._speaker_started:
            return
        self._speaker_started = False
        await self._transport.send_command(0x1001, _encrypt(_COMMAND.pack(0x108), self._key))

    async def read(self, *, timeout=15) -> VideoFrame:
        return await self._read(timeout, audio=False)

    async def read_media(self, *, timeout=15) -> VideoFrame | AudioPacket:
        return await self._read(timeout, audio=self._audio)

    async def _read(self, timeout, *, audio):
        async with asyncio.timeout(timeout):
            while True:
                channel, message = await self._transport.receive()
                if channel != 2:
                    continue
                if len(message) < 32:
                    raise CS2Error("truncated MISS media header")
                codec, = _CODEC.unpack_from(message)
                if codec in (4, 5):
                    return decode_video_frame(message[:32], message[32:], self._key)
                if audio and codec in AUDIO_CODECS:
                    with suppress(MediaFormatError):
                        return decode_audio_packet(message[:32], message[32:], self._key)


@asynccontextmanager
async def open_video(camera, source, lan, credentials, *, timeout=15, quality=0, preference="auto", audio=False):
    """Open a direct stream with bounded setup and deterministic cleanup."""
    transport = await open_transport(camera, source, lan, timeout=timeout, preference=preference)
    async with transport:
        async with asyncio.timeout(timeout):
            key = await _start(transport, credentials, quality, audio)
        video = C201Video(transport, key, audio=audio)
        try:
            yield video
        finally:
            with suppress(CS2Error, OSError, TimeoutError):
                async with asyncio.timeout(0.5):
                    await video.stop_speaker()
            with suppress(CS2Error, OSError, TimeoutError):
                async with asyncio.timeout(0.5):
                    await transport.send_command(0x1001, _encrypt(_COMMAND.pack(0x103), key))

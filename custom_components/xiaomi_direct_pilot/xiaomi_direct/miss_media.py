"""Parse access units delivered by the Xiaomi MISS media channel.

Header fields follow the MIT-licensed Go reference in
internal/xiaomi/protocol/miss/client.go. The transport supplies the header
and the nonce-prefixed ciphertext separately.
"""

from dataclasses import dataclass, field
import re
from struct import Struct
from typing import Literal

from cryptography.hazmat.primitives.ciphers import Cipher, algorithms


_FIELDS = Struct("<4xIIIQ")
_HEADER_SIZE = 32
_START_CODE = re.compile(rb"\x00\x00\x00?\x01")
AUDIO_CODECS = {1026: "pcmu", 1027: "pcma", 1032: "opus"}


class MediaFormatError(ValueError):
    """A MISS packet does not represent a usable video access unit."""


@dataclass(frozen=True, slots=True)
class VideoFrame:
    codec: Literal["h264", "h265"]
    sequence: int
    flags: int
    timestamp: int
    payload: bytes
    random_access: bool


@dataclass(frozen=True, slots=True)
class AudioPacket:
    codec: str
    sample_rate: int
    timestamp: int
    payload: bytes = field(repr=False)


def _decrypt(encrypted: bytes, session_key: bytes) -> bytes:
    if len(session_key) != 32:
        raise MediaFormatError("invalid MISS media session key size")
    if len(encrypted) <= 8:
        raise MediaFormatError("missing MISS media nonce or ciphertext")

    # Go's IETF ChaCha20 starts with a zero 32-bit counter and a nonce
    # whose first four bytes are zero. This is the same initial state as
    # a zero 64-bit counter followed by the eight wire nonce bytes.
    nonce = bytes(8) + encrypted[:8]
    decryptor = Cipher(algorithms.ChaCha20(session_key, nonce), mode=None).decryptor()
    return decryptor.update(encrypted[8:]) + decryptor.finalize()


def decode_video_frame(header: bytes, encrypted: bytes, session_key: bytes) -> VideoFrame:
    """Decrypt a MISS video packet with the already negotiated session key."""
    return parse_video_frame(header, _decrypt(encrypted, session_key))


def decode_audio_packet(header: bytes, encrypted: bytes, session_key: bytes) -> AudioPacket:
    """Decode the audio codec, rate flag and timestamp from the MISS header."""
    if len(header) != _HEADER_SIZE:
        raise MediaFormatError("invalid MISS media header size")
    codec_id, _, flags, timestamp = _FIELDS.unpack_from(header)
    if codec_id not in AUDIO_CODECS:
        raise MediaFormatError("unsupported MISS audio codec")
    codec = AUDIO_CODECS[codec_id]
    rate = 48000 if codec == "opus" else (16000 if (flags >> 3) & 0xF else 8000)
    return AudioPacket(codec, rate, timestamp, _decrypt(encrypted, session_key))


def parse_video_frame(header: bytes, payload: bytes) -> VideoFrame:
    """Translate the wire metadata while preserving the original Annex B data."""
    if len(header) != _HEADER_SIZE:
        raise MediaFormatError("invalid MISS media header size")
    codec_id, sequence, flags, timestamp = _FIELDS.unpack_from(header)
    if codec_id == 4:
        codec = "h264"
        nal_header_size = 1
    elif codec_id == 5:
        codec = "h265"
        nal_header_size = 2
    else:
        raise MediaFormatError("unsupported MISS video codec")

    random_access = False
    current_nal = None
    for start_code in _START_CODE.finditer(payload):
        if current_nal is None:
            if any(payload[:start_code.start()]):
                raise MediaFormatError("unexpected bytes before first Annex B NAL")
        elif start_code.start() - current_nal < nal_header_size:
            raise MediaFormatError("truncated Annex B NAL header")
        current_nal = start_code.end()
        if len(payload) - current_nal < nal_header_size:
            raise MediaFormatError("truncated Annex B NAL header")
        first_byte = payload[current_nal]
        if codec == "h264":
            random_access |= first_byte & 0x1F == 5
        else:
            random_access |= 16 <= (first_byte >> 1) & 0x3F <= 21
    if current_nal is None:
        raise MediaFormatError("no Annex B NAL in MISS video packet")

    return VideoFrame(codec, sequence, flags, timestamp, payload, random_access)

"""Offline media check inside the Home Assistant image (requires FFmpeg)."""

import struct
import subprocess
import unittest

from cryptography.hazmat.primitives.ciphers import Cipher, algorithms

from custom_components.xiaomi_direct_pilot.xiaomi_direct.miss_media import decode_video_frame


def ffmpeg(*args, source=None):
    return subprocess.run(
        ("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", *args),
        input=source,
        capture_output=True,
        check=True,
        timeout=15,
    ).stdout


class DirectMediaDecodeTest(unittest.TestCase):
    def test_synthetic_hevc_access_unit_decodes_after_miss_decryption(self):
        width = height = 64
        source = ffmpeg(
            "-f", "lavfi", "-i", "color=red:size=64x64",
            "-frames:v", "1", "-c:v", "libx265",
            "-x265-params", "pools=none:frame-threads=1:log-level=error",
            "-f", "hevc", "pipe:1",
        )
        key = bytes(range(32))
        nonce = bytes(range(1, 9))
        cipher = Cipher(algorithms.ChaCha20(key, bytes(8) + nonce), mode=None)
        encryptor = cipher.encryptor()
        encrypted = nonce + encryptor.update(source) + encryptor.finalize()
        frame = decode_video_frame(struct.pack("<4xIIIQ8x", 5, 1, 0, 1000), encrypted, key)
        self.assertTrue(frame.random_access)
        raw = ffmpeg(
            "-f", "hevc", "-i", "pipe:0",
            "-frames:v", "1", "-pix_fmt", "yuv420p", "-f", "rawvideo", "pipe:1",
            source=frame.payload,
        )
        self.assertEqual(len(raw), width * height * 3 // 2)


if __name__ == "__main__":
    unittest.main()

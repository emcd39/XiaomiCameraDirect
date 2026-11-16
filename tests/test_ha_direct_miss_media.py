import struct
import unittest

from xiaomi_direct.miss_media import MediaFormatError, decode_video_frame, parse_video_frame


def media_header(codec_id):
    return struct.pack("<4xIIIQ8x", codec_id, 27, 0x70000, 2**34 + 123)


class MissMediaTest(unittest.TestCase):
    def test_decodes_go_reference_ciphertext(self):
        # Generated with Go x/crypto/chacha20 and a synthetic key and nonce.
        data = bytes.fromhex("01020304050607088cea583e8789c036cbaeb8a43fedd2e286d6c3")
        frame = decode_video_frame(media_header(5), data, bytes(range(32)))
        self.assertEqual(frame.payload, bytes.fromhex("000000014001aa0000014201aa0000012601aa"))
        self.assertTrue(frame.random_access)

    def test_rejects_incomplete_encrypted_packets_and_keys(self):
        cases = ((bytes(8), bytes(32)), (bytes(9), bytes(31)))
        for data, key in cases:
            with self.subTest(packet_length=len(data), key_length=len(key)):
                with self.assertRaises(MediaFormatError):
                    decode_video_frame(media_header(5), data, key)

    def test_h265_frame_preserves_metadata_and_payload(self):
        payload = b"\x00\x00\x00\x01\x40\x01\xAA\x00\x00\x01\x26\x01\xAA"
        frame = parse_video_frame(media_header(5), payload)
        self.assertEqual(frame.codec, "h265")
        self.assertEqual(frame.sequence, 27)
        self.assertEqual(frame.flags, 0x70000)
        self.assertEqual(frame.timestamp, 2**34 + 123)
        self.assertIs(frame.payload, payload)
        self.assertTrue(frame.random_access)

    def test_parameter_sets_do_not_count_as_a_random_access_picture(self):
        vps = b"\x00\x00\x01\x40\x01\xAA"
        sps = b"\x00\x00\x00\x01\x42\x01\xAA"
        self.assertFalse(parse_video_frame(media_header(5), vps + sps).random_access)

    def test_h264_distinguishes_idr_from_other_slices(self):
        for nal, expected in ((b"\x65", True), (b"\x61", False)):
            with self.subTest(nal=nal):
                frame = parse_video_frame(media_header(4), b"\x00\x00\x01" + nal + b"\xAA")
                self.assertEqual(frame.codec, "h264")
                self.assertEqual(frame.random_access, expected)

    def test_bad_headers_and_truncated_nals_are_rejected(self):
        valid_nal = b"\x00\x00\x01\x42\x01\xAA"
        for header, payload in (
            (media_header(5)[:-1], valid_nal),
            (media_header(1032), valid_nal),
            (media_header(5), b"not Annex B"),
            (media_header(5), valid_nal + b"\x00\x00\x01\x26"),
            (media_header(5), b"\x00\x00\x01" + valid_nal),
        ):
            with self.subTest(header=header, payload=payload):
                with self.assertRaises(MediaFormatError):
                    parse_video_frame(header, payload)

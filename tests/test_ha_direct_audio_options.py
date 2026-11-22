"""Regression coverage for model-specific microphone opt-ins."""

import unittest

from custom_components.xiaomi_direct_pilot.camera import camera_stream_settings
from custom_components.xiaomi_direct_pilot.const import (
    AW300_AUDIO, AW300_MODEL, C201_AUDIO, C201_MODEL, C400_AUDIO, C400_LOW_RESOLUTION, C400_MODEL, C701_AUDIO, C701_MODEL,
)


class CameraStreamSettingsTest(unittest.TestCase):
    def test_aw300_audio_is_opt_in_and_keeps_auto_quality(self):
        self.assertEqual(camera_stream_settings(AW300_MODEL, "aw300", "de", {}), (0, False))
        self.assertEqual(camera_stream_settings(AW300_MODEL, "aw300", "de", {AW300_AUDIO: ["aw300"]}), (0, True))
        self.assertEqual(camera_stream_settings(C400_MODEL, "aw300", "de", {AW300_AUDIO: ["aw300"]}), (3, False))

    def test_c400_audio_is_opt_in_and_uses_the_selected_video_profile(self):
        self.assertEqual(camera_stream_settings(C400_MODEL, "c400", "de", {}), (3, False))
        options = {C400_AUDIO: ["c400"]}
        self.assertEqual(camera_stream_settings(C400_MODEL, "c400", "de", options), (3, True))
        self.assertEqual(
            camera_stream_settings(C400_MODEL, "c400", "de", {**options, C400_LOW_RESOLUTION: ["c400"]}),
            (2, True),
        )
        self.assertEqual(camera_stream_settings(C400_MODEL, "c400", "us", options), (3, False))
        self.assertEqual(camera_stream_settings(C201_MODEL, "c400", "de", options), (0, False))

    def test_c701_audio_is_opt_in_and_keeps_the_4k_profile(self):
        self.assertEqual(camera_stream_settings(C701_MODEL, "c701", "de", {}), (0, False))
        self.assertEqual(
            camera_stream_settings(C701_MODEL, "c701", "de", {C701_AUDIO: ["c701"]}),
            (0, True),
        )

    def test_c701_audio_option_does_not_enable_other_models_or_regions(self):
        options = {C701_AUDIO: ["c701"]}
        self.assertEqual(camera_stream_settings(C201_MODEL, "c201", "de", options), (0, False))
        self.assertEqual(camera_stream_settings(C701_MODEL, "c701", "us", options), (0, False))

    def test_c201_microphone_keeps_its_verified_profile(self):
        self.assertEqual(
            camera_stream_settings(C201_MODEL, "c201", "de", {C201_AUDIO: ["c201"]}),
            (2, True),
        )

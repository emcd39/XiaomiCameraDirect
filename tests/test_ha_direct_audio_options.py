"""Regression coverage for model-specific microphone opt-ins."""

import unittest

from custom_components.xiaomi_direct_pilot.camera import camera_stream_settings
from custom_components.xiaomi_direct_pilot.const import C201_AUDIO, C201_MODEL, C701_AUDIO, C701_MODEL


class CameraStreamSettingsTest(unittest.TestCase):
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

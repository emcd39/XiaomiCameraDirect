"""Direct camera inventory and route refresh behavior."""

import unittest

from custom_components.xiaomi_direct_pilot.button import PTZ_MODELS
from custom_components.xiaomi_direct_pilot.const import C701_MODEL
from custom_components.xiaomi_direct_pilot.inventory import QUALITY, merge_refreshed_camera


class DirectCameraInventoryTest(unittest.TestCase):
    def test_c701_has_a_baseline_stream_profile_and_ptz(self):
        self.assertEqual(QUALITY[C701_MODEL], 0)
        self.assertIn(C701_MODEL, PTZ_MODELS)

    def test_refresh_drops_saved_route_even_when_camera_ip_is_unchanged(self):
        camera = {
            "device_id": "camera-1", "camera_ip": "192.168.2.124",
        }
        saved = {
            **camera, "source_ip": "192.168.2.145", "camera_lan": "192.168.2.0/24",
        }
        self.assertEqual(merge_refreshed_camera(camera, saved), camera)

    def test_refresh_uses_currently_resolved_route(self):
        camera = {
            "device_id": "camera-1", "camera_ip": "192.168.2.201",
            "source_ip": "192.168.2.80", "camera_lan": "192.168.2.0/24",
        }
        saved = {
            "device_id": "camera-1", "camera_ip": "192.168.2.124",
            "source_ip": "192.168.2.145", "camera_lan": "192.168.2.0/24",
        }
        self.assertEqual(merge_refreshed_camera(camera, saved), camera)

    def test_refresh_does_not_restore_saved_camera_ip_when_current_ip_is_missing(self):
        camera = {"device_id": "camera-1"}
        saved = {
            "device_id": "camera-1", "camera_ip": "192.168.2.124",
            "source_ip": "192.168.2.145", "camera_lan": "192.168.2.0/24",
        }
        self.assertEqual(merge_refreshed_camera(camera, saved), camera)

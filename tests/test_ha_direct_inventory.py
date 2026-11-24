"""Direct camera inventory and route refresh behavior."""

import unittest

from custom_components.xiaomi_direct_pilot.button import PTZ_MODELS
from custom_components.xiaomi_direct_pilot.const import C701_MODEL, PTZ2K_MODEL, PTZ3_MODEL
from custom_components.xiaomi_direct_pilot.inventory import QUALITY, account_cameras, excluded_camera_ids, merge_refreshed_camera


class DirectCameraInventoryTest(unittest.TestCase):
    def test_new_model_is_in_inventory_without_a_transport_profile(self):
        cameras = account_cameras({"list": [
            {"model": "test.camera.new", "did": "new", "name": "New camera"},
            {"model": "test.plug.new", "did": "plug"},
        ]})
        self.assertEqual(list(cameras), ["new"])
        self.assertNotIn(cameras["new"]["model"], QUALITY)

    def test_explicit_exclusions_survive_inventory_changes(self):
        saved = [{"device_id": "selected"}, {"device_id": "excluded"}]
        excluded = excluded_camera_ids(saved, {"camera_ids": ["selected"]})
        self.assertEqual(excluded, {"excluded"})
        refreshed = [{"device_id": "selected"}, {"device_id": "new"}]
        self.assertEqual(excluded_camera_ids(refreshed, {"excluded_camera_ids": sorted(excluded)}), excluded)

    def test_c701_has_a_baseline_stream_profile_and_ptz(self):
        self.assertEqual(QUALITY[C701_MODEL], 0)
        self.assertIn(C701_MODEL, PTZ_MODELS)

    def test_cn_ptz_models_have_a_stream_profile_and_ptz(self):
        # Model IDs confirmed from a real cn account inventory (did 449045561 / 1106518806).
        # Quality 2 is go2rtc's default for models without a specific override.
        for model in (PTZ2K_MODEL, PTZ3_MODEL):
            self.assertEqual(QUALITY[model], 2)
            self.assertIn(model, PTZ_MODELS)

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

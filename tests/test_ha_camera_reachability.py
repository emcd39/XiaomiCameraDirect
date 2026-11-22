"""Dynamic route and CS2 checks for cameras already configured in Home Assistant."""

import json
from ipaddress import IPv4Address, IPv4Network
from pathlib import Path
import tempfile
import unittest
from unittest.mock import MagicMock, patch

from scripts import ha_camera_reachability as reachability


CAMERA = IPv4Address("192.168.50.5")
SOURCE = IPv4Address("192.168.50.20")
NETWORK = IPv4Network("192.168.50.0/24")


class CameraReachabilityTest(unittest.TestCase):
    def test_model_filter_sends_no_probe_to_other_cameras(self):
        c701 = {"model": "chuangmi.camera.079ae2"}
        c201 = {"model": "xiaomi.camera.c201"}
        with (
            patch.object(reachability, "load_camera_inventory", return_value=[c201, c701]),
            patch.object(reachability, "inspect_camera", return_value={"direct_usable": True, "status": "available"}) as probe,
        ):
            report = reachability.build_report(Path("config"), 2, model=c701["model"])
            probe.assert_called_once_with(c701, 2)
            self.assertEqual(report["summary"]["camera_count"], 1)
            probe.reset_mock()
            report = reachability.build_report(Path("config"), 2, model="missing")
            probe.assert_not_called()
            self.assertEqual(report["cameras"], [])

    def test_loads_only_sanitized_camera_inventory_from_ha_storage(self):
        entry = {
            "domain": "xiaomi_direct_pilot",
            "data": {
                "account_pass_token": "must-not-be-returned",
                "cameras": [{
                    "name": "Test C701", "model": "chuangmi.camera.079ae2",
                    "device_id": "device-1", "camera_ip": str(CAMERA),
                    "camera_lan": str(NETWORK), "source_ip": str(SOURCE),
                }],
            },
        }
        with tempfile.TemporaryDirectory() as temporary:
            storage = Path(temporary) / ".storage"
            storage.mkdir()
            (storage / "core.config_entries").write_text(
                json.dumps({"data": {"entries": [entry]}}), encoding="utf-8"
            )
            inventory = reachability.load_camera_inventory(Path(temporary))
        self.assertEqual(len(inventory), 1)
        self.assertEqual(inventory[0]["camera_ip"], CAMERA)
        self.assertNotIn("account_pass_token", inventory[0])
        self.assertNotIn("device_id", inventory[0])

    @patch.object(reachability, "probe_cs2_unicast", side_effect=[True, True, True])
    @patch.object(reachability, "route_source", return_value=SOURCE)
    def test_mixed_inventory_reports_bad_cameras_and_still_probes_valid_one(self, route, probe):
        entry = {
            "domain": "xiaomi_direct_pilot",
            "data": {"cameras": [
                {
                    "name": "Valid camera", "model": "test.model", "device_id": "ok",
                    "camera_ip": str(CAMERA), "camera_lan": str(NETWORK), "source_ip": str(SOURCE),
                },
                {
                    "name": "No camera LAN", "model": "test.model", "device_id": "missing-lan",
                    "camera_ip": "192.168.50.6", "source_ip": str(SOURCE),
                },
                {
                    "name": "Invalid camera LAN", "model": "test.model", "device_id": "invalid-lan",
                    "camera_ip": "192.168.50.7", "camera_lan": "not-a-subnet", "source_ip": str(SOURCE),
                },
                {
                    "name": "Empty camera IP", "model": "test.model", "device_id": "empty-ip",
                    "camera_ip": "", "camera_lan": str(NETWORK), "source_ip": str(SOURCE),
                },
                {
                    "name": "Invalid camera IP", "model": "test.model", "device_id": "invalid-ip",
                    "camera_ip": "not-an-ip", "camera_lan": str(NETWORK), "source_ip": str(SOURCE),
                },
                {
                    "name": "Public camera IP", "model": "test.model", "device_id": "public-ip",
                    "camera_ip": "8.8.8.8", "camera_lan": "8.8.8.0/24", "source_ip": str(SOURCE),
                },
            ]},
        }
        with tempfile.TemporaryDirectory() as temporary:
            storage = Path(temporary) / ".storage"
            storage.mkdir()
            (storage / "core.config_entries").write_text(
                json.dumps({"data": {"entries": [entry]}}), encoding="utf-8"
            )
            report = reachability.build_report(Path(temporary), timeout=2)

        by_name = {camera["name"]: camera for camera in report["cameras"]}
        self.assertEqual(report["summary"]["camera_count"], 6)
        self.assertEqual(by_name["Valid camera"]["status"], "available")
        missing_lan = by_name["No camera LAN"]
        self.assertEqual(missing_lan["status"], "cs2_reply_missing_camera_lan")
        self.assertEqual(missing_lan["inventory_errors"], ["missing_camera_lan"])
        self.assertEqual(missing_lan["cs2_result"], "reply")
        self.assertFalse(missing_lan["direct_usable"])
        invalid_lan = by_name["Invalid camera LAN"]
        self.assertEqual(invalid_lan["status"], "cs2_reply_invalid_camera_lan")
        self.assertEqual(invalid_lan["cs2_result"], "reply")
        self.assertFalse(invalid_lan["direct_usable"])
        self.assertEqual(by_name["Empty camera IP"]["status"], "missing_camera_ip")
        self.assertEqual(by_name["Invalid camera IP"]["status"], "invalid_camera_ip")
        self.assertEqual(by_name["Public camera IP"]["status"], "non_private_camera_ip")
        self.assertEqual(probe.call_count, 3)
        self.assertEqual(route.call_count, 3)
        json.dumps(report)

    @patch.object(reachability, "probe_cs2_unicast", return_value=True)
    @patch.object(reachability, "route_source", return_value=SOURCE)
    def test_reports_camera_available_when_route_and_cs2_are_valid(self, route, probe):
        camera = {
            "name": "Test C701", "model": "test.model", "camera_ip": CAMERA,
            "camera_lan": NETWORK, "configured_source_ip": SOURCE,
        }
        result = reachability.inspect_camera(camera, 1)
        self.assertEqual(result["status"], "available")
        self.assertTrue(result["direct_usable"])
        probe.assert_called_once_with(CAMERA, SOURCE, 1)

    @patch.object(reachability, "probe_cs2_unicast", return_value=False)
    @patch.object(reachability, "route_source", return_value=IPv4Address("192.168.3.178"))
    def test_reports_out_of_subnet_source_even_when_routed_probe_times_out(self, route, probe):
        camera = {
            "name": "Test C201", "model": "test.model", "camera_ip": CAMERA,
            "camera_lan": NETWORK, "configured_source_ip": IPv4Address("192.168.50.20"),
        }
        result = reachability.inspect_camera(camera, 1)
        self.assertEqual(result["status"], "source_outside_camera_subnet")
        self.assertFalse(result["direct_usable"])
        probe.assert_called_once()

    @patch.object(reachability.socket, "socket")
    def test_probe_sends_one_unicast_packet_and_accepts_valid_cs2_reply(self, socket_factory):
        sock = socket_factory.return_value.__enter__.return_value
        sock.recvfrom.return_value = (b"\xf1\x41\x00\x00", (str(CAMERA), 32108))
        self.assertTrue(reachability.probe_cs2_unicast(CAMERA, SOURCE, 1))
        sock.bind.assert_called_once_with((str(SOURCE), 0))
        sock.sendto.assert_called_once_with(reachability.LAN_SEARCH, (str(CAMERA), 32108))
        sock.setsockopt.assert_not_called()

    @patch.object(reachability.socket, "socket")
    def test_probe_reports_timeout_without_retrying(self, socket_factory):
        sock = socket_factory.return_value.__enter__.return_value
        sock.recvfrom.side_effect = TimeoutError
        self.assertFalse(reachability.probe_cs2_unicast(CAMERA, SOURCE, 1))
        sock.sendto.assert_called_once()
        sock.recvfrom.assert_called_once()


if __name__ == "__main__":
    unittest.main()

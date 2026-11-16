from ipaddress import IPv4Address, IPv4Network
import unittest
from unittest.mock import MagicMock, patch

from scripts import ha_direct_preflight as preflight


CAMERA = IPv4Address("192.168.50.5")
SOURCE = IPv4Address("192.168.50.20")
NETWORK = IPv4Network("192.168.50.0/24")


class PreflightTest(unittest.TestCase):
    @patch.object(preflight, "route_source")
    def test_rejects_out_of_scope_targets_before_route_lookup(self, route):
        for camera in ("203.0.113.1", "192.168.50.0", "192.168.50.255"):
            with self.subTest(camera=camera):
                self.assertEqual(
                    preflight.preflight(IPv4Address(camera), NETWORK),
                    ("invalid_camera_subnet", 2),
                )
        route.assert_not_called()

    @patch.object(preflight, "find_camera")
    @patch.object(preflight, "route_source", return_value=IPv4Address("172.18.0.2"))
    def test_nat_source_blocks_probe(self, route, find_camera):
        self.assertEqual(
            preflight.preflight(CAMERA, NETWORK, probe=True),
            ("source_outside_camera_subnet", 2),
        )
        find_camera.assert_not_called()

    @patch.object(preflight, "find_camera")
    @patch.object(preflight, "route_source", return_value=SOURCE)
    def test_route_only_does_not_send(self, route, find_camera):
        self.assertEqual(preflight.preflight(CAMERA, NETWORK), ("route_candidate", 0))
        find_camera.assert_not_called()

    @patch.object(preflight.socket, "socket")
    def test_ignores_wrong_peer_and_invalid_framing(self, socket_factory):
        sock = MagicMock()
        socket_factory.return_value.__enter__.return_value = sock
        sock.recvfrom.side_effect = [
            (b"\xf1\x41\x00\x00", ("192.168.50.7", 32108)),
            (b"\xf1\x41", (str(CAMERA), 32108)),
            (b"\xf1\x41\x00\x01", (str(CAMERA), 32108)),
            (b"\xf1\x42\x00\x00", (str(CAMERA), 32108)),
            (b"\xf1\x41\x00\x00", (str(CAMERA), 50000)),
        ]
        self.assertTrue(preflight.find_camera(CAMERA, NETWORK, SOURCE, 5))
        self.assertEqual(sock.recvfrom.call_count, 5)
        self.assertEqual(
            [call.args for call in sock.sendto.call_args_list],
            [
                (preflight.LAN_SEARCH, (str(CAMERA), 32108)),
                (preflight.LAN_SEARCH, (str(NETWORK.broadcast_address), 32108)),
            ],
        )
        socket_factory.return_value.__exit__.assert_called_once()

    @patch.object(preflight.socket, "socket")
    def test_socket_timeout_is_not_reported_as_reply(self, socket_factory):
        sock = socket_factory.return_value.__enter__.return_value
        sock.recvfrom.side_effect = TimeoutError
        self.assertFalse(preflight.find_camera(CAMERA, NETWORK, SOURCE, 5))
        socket_factory.return_value.__exit__.assert_called_once()

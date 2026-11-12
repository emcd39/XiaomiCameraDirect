"""Focused behavior checks for the HA camera entity's changing stream source."""
from __future__ import annotations

import asyncio
from pathlib import Path
import sys
import unittest
from types import SimpleNamespace

from homeassistant.components.camera import CameraEntityFeature

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from custom_components.xiaomi_local_gateway.camera import GatewayCamera


class CameraEntityTests(unittest.TestCase):
    def test_stream_feature_appears_when_coordinator_receives_rtsp_source(self) -> None:
        coordinator = SimpleNamespace(data={"late_camera": {"rtsp_url": None}})
        camera = GatewayCamera.__new__(GatewayCamera)
        object.__setattr__(camera, "coordinator", coordinator)
        object.__setattr__(camera, "did", "late_camera")

        self.assertEqual(camera.supported_features, CameraEntityFeature(0))
        self.assertIsNone(asyncio.run(camera.stream_source()))

        source = "rtsp://camera-mock:8554/late-camera"
        coordinator.data["late_camera"]["rtsp_url"] = source

        self.assertEqual(camera.supported_features, CameraEntityFeature.STREAM)
        self.assertEqual(asyncio.run(camera.stream_source()), source)

        coordinator.data["late_camera"]["rtsp_url"] = None
        self.assertEqual(camera.supported_features, CameraEntityFeature(0))


if __name__ == "__main__":
    unittest.main()

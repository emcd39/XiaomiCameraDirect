"""Focused behavior checks for the HA camera entity's changing stream source."""
from __future__ import annotations

import asyncio
import unittest
from types import SimpleNamespace

from homeassistant.components.camera import CameraEntityFeature
from homeassistant.exceptions import HomeAssistantError
from gateway_test_path import add_gateway_component_path

add_gateway_component_path()

from xiaomi_local_gateway.camera import GatewayCamera
from xiaomi_local_gateway.api import GatewayError, GatewayPTZSessionUnavailable
from xiaomi_local_gateway.button import PTZButton


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

    def test_support_attribute_includes_machine_reason_and_readable_text(self) -> None:
        coordinator = SimpleNamespace(data={"c201": {
            "model": "xiaomi.camera.c201",
            "support": {
                "schema_version": 1,
                "features": {
                    "camera_settings_read": {
                        "implementation": "implemented",
                        "validation": "offline",
                        "path": "cloud",
                        "availability": "unknown",
                        "reason": "unverified_firmware",
                        "requires_cloud": ["account_auth", "inventory", "settings_read"],
                    },
                },
            },
        }})
        camera = GatewayCamera.__new__(GatewayCamera)
        object.__setattr__(camera, "coordinator", coordinator)
        object.__setattr__(camera, "did", "c201")

        support = camera.extra_state_attributes["support"]
        detail = support["features"]["camera_settings_read"]
        self.assertEqual(detail["availability"], "unknown")
        self.assertEqual(detail["reason"], "unverified_firmware")
        self.assertIn("firmware", detail["reason_text"].lower())


class ColdCameraTests(unittest.IsolatedAsyncioTestCase):
    async def test_ptz_only_prepares_after_confirmed_missing_session(self) -> None:
        for error, expected in (
            (None, ["step"]),
            (GatewayPTZSessionUnavailable("No session"), ["step", "stream", "step"]),
            (GatewayError("Request timed out"), ["step"]),
        ):
            with self.subTest(error=error):
                actions = []

                async def stream(camera_id):
                    actions.append("stream")

                async def step(camera_id, direction, *, managed):
                    actions.append("step")
                    if len(actions) == 1 and error is not None:
                        raise error

                coordinator = SimpleNamespace(
                    data={"c201": {"on_demand_stream": True, "managed_steps": True, "ptz": True}},
                    client=SimpleNamespace(camera_stream=stream, ptz_step=step),
                )
                button = PTZButton(coordinator, "c201", "left")
                if type(error) is GatewayError:
                    with self.assertRaises(HomeAssistantError):
                        await button.async_press()
                else:
                    await button.async_press()
                self.assertEqual(actions, expected)

    async def test_failed_preparation_never_sends_a_ptz_step(self) -> None:
        actions = []

        async def stream(camera_id):
            actions.append(("stream", camera_id))
            raise GatewayError("Camera stream timed out")

        async def step(camera_id, direction, *, managed):
            actions.append(("step", camera_id, direction, managed))
            raise GatewayPTZSessionUnavailable("No session")

        coordinator = SimpleNamespace(
            data={"c201": {"on_demand_stream": True, "managed_steps": True, "ptz": True}},
            client=SimpleNamespace(camera_stream=stream, ptz_step=step),
        )
        button = PTZButton(coordinator, "c201", "left")

        with self.assertRaisesRegex(HomeAssistantError, "Camera stream timed out"):
            await button.async_press()
        self.assertEqual(actions, [("step", "c201", "left", True), ("stream", "c201")])


if __name__ == "__main__":
    unittest.main()

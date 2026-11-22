"""Shared Home Assistant backend contracts and gateway adapter tests."""

import unittest
from unittest.mock import AsyncMock

from custom_components.xiaomi_local_gateway.backend import (
    CameraBackend,
    CameraReadBackend,
    GatewayBackendAdapter,
)
from custom_components.xiaomi_local_gateway.direct_backend import DirectBackendAdapter


class GatewayBackendContractTest(unittest.IsolatedAsyncioTestCase):
    async def test_gateway_adapter_implements_camera_and_read_contracts(self):
        client = AsyncMock()
        camera = {"id": "c201", "name": "C201", "model": "xiaomi.camera.c201", "ptz": True}
        client.cameras.return_value = [camera]
        client.camera_stream.return_value = "rtsp://example.invalid/c201"
        client.camera_read_capabilities.return_value = {"sd_capacity"}
        client.camera_settings.return_value = {"properties": {}}
        client.camera_storage.return_value = {"properties": {}}

        backend = GatewayBackendAdapter(client)

        self.assertIsInstance(backend, CameraBackend)
        self.assertIsInstance(backend, CameraReadBackend)
        self.assertEqual(await backend.cameras(), [camera])
        self.assertEqual(await backend.camera_stream("c201"), "rtsp://example.invalid/c201")
        await backend.ptz_step("c201", "left", managed=True)
        self.assertEqual(await backend.camera_read_capabilities(), {"sd_capacity"})
        self.assertEqual(await backend.camera_settings("c201"), {"properties": {}})
        self.assertEqual(await backend.camera_storage("c201"), {"properties": {}})
        client.cameras.assert_awaited_once_with()
        client.camera_stream.assert_awaited_once_with("c201")
        client.ptz_step.assert_awaited_once_with("c201", "left", managed=True)
        client.camera_read_capabilities.assert_awaited_once_with()
        client.camera_settings.assert_awaited_once_with("c201")
        client.camera_storage.assert_awaited_once_with("c201")

    async def test_inventory_does_not_open_a_stream(self):
        client = AsyncMock()
        client.cameras.return_value = [{"id": "c701", "name": "C701", "model": "chuangmi.camera.079ae2"}]
        backend = GatewayBackendAdapter(client)

        self.assertEqual(len(await backend.cameras()), 1)
        client.camera_stream.assert_not_awaited()


class DirectBackendContractTest(unittest.IsolatedAsyncioTestCase):
    async def test_direct_adapter_matches_camera_contract_and_releases_media(self):
        client = AsyncMock()
        account = object()
        media = AsyncMock()
        media.url = None
        async def start_media():
            media.url = "http://127.0.0.1:8123/api/xiaomi/live.m3u8"
        media.start.side_effect = start_media
        control = AsyncMock()
        backend = DirectBackendAdapter(
            client,
            account,
            [{"device_id": "same-camera-id", "name": "C201", "model": "xiaomi.camera.c201"}],
            {"same-camera-id": media},
            {"same-camera-id": control},
        )

        self.assertIsInstance(backend, CameraBackend)
        self.assertIsInstance(backend, CameraReadBackend)
        cameras = await backend.cameras()
        self.assertEqual(cameras, [{
            "id": "same-camera-id", "name": "C201", "model": "xiaomi.camera.c201",
            "ptz": True, "on_demand_stream": True, "available": True, "direct_status": "ready",
        }])
        self.assertEqual(await backend.camera_stream("same-camera-id"), media.url)
        media.start.assert_awaited_once_with()
        await backend.ptz_step("same-camera-id", "left", managed=True)
        control.press.assert_awaited_once_with("left")
        await backend.close()
        media.close.assert_awaited_once_with()
        client.close.assert_awaited_once_with()
        # Inventory is deliberately metadata-only; the media source starts
        # only when HA asks for a stream.
        media.start.reset_mock()
        await backend.cameras()
        media.start.assert_not_awaited()

    async def test_direct_inventory_marks_missing_route_without_stream_support(self):
        backend = DirectBackendAdapter(
            AsyncMock(), object(),
            [{"device_id": "no-route", "name": "Offline camera", "model": "xiaomi.camera.c201"}],
            {}, {}, {"no-route": "no_current_route"},
        )
        self.assertEqual((await backend.cameras())[0]["direct_status"], "no_current_route")
        self.assertFalse((await backend.cameras())[0]["available"])

    async def test_direct_id_alias_keeps_gateway_entity_identity(self):
        control = AsyncMock()
        backend = DirectBackendAdapter(
            AsyncMock(), object(),
            [{"device_id": "xiaomi-did", "backend_id": "legacy-gateway-id",
              "name": "C201", "model": "xiaomi.camera.c201"}],
            {"legacy-gateway-id": AsyncMock()},
            {"legacy-gateway-id": control},
        )
        camera = (await backend.cameras())[0]
        self.assertEqual(camera["id"], "legacy-gateway-id")
        await backend.ptz_step("legacy-gateway-id", "right")
        control.press.assert_awaited_once_with("right")


if __name__ == "__main__":
    unittest.main()

"""Behavioral tests for malformed and valid gateway responses."""
from __future__ import annotations

import unittest
import json
from pathlib import Path
import sys

from aiohttp import ClientSession, web
from aiohttp.test_utils import TestServer

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "custom_components"))

from xiaomi_local_gateway.api import (
    GatewayClient,
    GatewayError,
    GatewayUnauthorized,
)


class GatewayClientTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self) -> None:
        self.body = '{"cameras": []}'
        self.status = 200
        self.managed_body = '{"cameras": []}'
        self.managed_status = 404
        self.info_body = '{"features": []}'
        self.info_status = 200
        self.settings_body = json.dumps({
            "observed_at": "2026-11-12T12:00:00Z",
            "properties": {
                "night_shot": {"code": 0, "value": 2},
                "motion_detection": {"code": 0, "value": True},
            },
        })
        self.settings_requests = []

        async def inventory(request):
            if request.path == "/v2/cameras":
                return web.Response(text=self.managed_body, status=self.managed_status, content_type="application/json")
            return web.Response(text=self.body, status=self.status, content_type="application/json")

        async def info(request):
            if request.headers.get("Authorization") != "Bearer test-token":
                return web.Response(status=401)
            return web.Response(text=self.info_body, status=self.info_status, content_type="application/json")

        async def settings(request):
            self.settings_requests.append((request.match_info["camera_id"], request.headers.get("Authorization")))
            return web.Response(text=self.settings_body, content_type="application/json")

        async def snapshot(request):
            if request.headers.get("Authorization") != "Bearer test-token":
                return web.Response(status=401)
            return web.Response(body=b"\xff\xd8\x01\xff\xd9", content_type="image/jpeg")

        app = web.Application()
        app.router.add_get("/v1/cameras", inventory)
        app.router.add_get("/v2/cameras", inventory)
        app.router.add_get("/v2/info", info)
        app.router.add_get("/v2/cameras/{camera_id}/settings", settings)
        app.router.add_get("/v2/cameras/{camera_id}/snapshot", snapshot)
        self.server = TestServer(app)
        await self.server.start_server()
        self.session = ClientSession()
        self.client = GatewayClient(self.session, str(self.server.make_url("")), "test-token")

    async def asyncTearDown(self) -> None:
        await self.session.close()
        await self.server.close()

    async def test_empty_inventory_is_valid(self) -> None:
        self.managed_status = 200
        self.assertEqual(await self.client.cameras(), [])

    async def test_managed_camera_exposes_stream_source_and_only_declared_controls(self) -> None:
        self.managed_status = 200
        self.managed_body = json.dumps({"cameras": [{
            "id": "c201-test", "name": "C201 test", "model": "Xiaomi C201",
            "capabilities": {"video": True, "ptz": False},
            "streams": [{"id": "main", "protocol": "rtsp", "url": "rtsp://gateway:8554/c201-test"}],
        }]})
        cameras = await self.client.cameras()
        self.assertEqual(cameras, [{
            "id": "c201-test", "name": "C201 test", "model": "Xiaomi C201",
            "rtsp_url": "rtsp://gateway:8554/c201-test", "ptz": False,
        }])

    async def test_managed_camera_without_stream_or_ptz_does_not_invent_features(self) -> None:
        self.managed_status = 200
        self.managed_body = json.dumps({"cameras": [{
            "id": "offline", "name": "Offline", "capabilities": {"video": True, "ptz": False},
            "video": {"state": "offline"}, "streams": [],
        }]})
        cameras = await self.client.cameras()
        self.assertIsNone(cameras[0]["rtsp_url"])
        self.assertFalse(cameras[0]["ptz"])

    async def test_managed_stream_credentials_are_rejected(self) -> None:
        self.managed_status = 200
        self.managed_body = json.dumps({"cameras": [{
            "id": "camera", "name": "Camera", "capabilities": {"ptz": False},
            "streams": [{"id": "main", "protocol": "rtsp", "url": "rtsp://user:secret@gateway/live"}],
        }]})
        with self.assertRaises(GatewayError):
            await self.client.cameras()

    async def test_invalid_json_becomes_gateway_error(self) -> None:
        self.body = "{not-json"
        with self.assertRaises(GatewayError):
            await self.client.cameras()

    async def test_malformed_inventory_is_rejected(self) -> None:
        bad_bodies = (
            '[]',
            '{"cameras": [{"id":"garden","rtsp_url":"rtsp://cam/live","ptz":"yes"}]}',
            '{"cameras": [{"id":"garden","rtsp_url":"rtsp://cam/live","ptz":false},'
            '{"id":"garden","rtsp_url":"rtsp://cam/other","ptz":false}]}',
        )
        for self.body in bad_bodies:
            with self.subTest(body=self.body), self.assertRaises(GatewayError):
                await self.client.cameras()

    async def test_unauthorized_response_remains_distinct(self) -> None:
        self.managed_status = 401
        with self.assertRaises(GatewayUnauthorized):
            await self.client.cameras()

    async def test_managed_snapshot_returns_jpeg_with_gateway_authorization(self) -> None:
        self.assertEqual(await self.client.snapshot("c201-test"), b"\xff\xd8\x01\xff\xd9")

    async def test_camera_settings_are_parsed_and_authorized(self) -> None:
        settings = await self.client.camera_settings("c201-test")
        self.assertEqual(settings["properties"]["night_shot"], {"code": 0, "value": 2})
        self.assertEqual(settings["properties"]["motion_detection"], {"code": 0, "value": True})
        self.assertEqual(self.settings_requests, [("c201-test", "Bearer test-token")])

    async def test_settings_support_check_retries_after_transient_info_failure(self) -> None:
        self.info_status = 503
        self.info_body = '{"error":"backend_unavailable"}'
        self.assertFalse(await self.client.supports_camera_settings())

        self.info_status = 200
        self.info_body = '{"features":["camera_selection","camera_settings_read"]}'
        self.assertTrue(await self.client.supports_camera_settings())
        self.assertEqual(self.settings_requests, [])

    async def test_no_settings_feature_does_not_call_settings_route(self) -> None:
        self.info_body = '{"features":["camera_selection"]}'
        self.assertFalse(await self.client.supports_camera_settings())
        self.assertEqual(self.settings_requests, [])


if __name__ == "__main__":
    unittest.main()

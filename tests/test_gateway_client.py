"""Behavioral tests for malformed and valid gateway responses."""
from __future__ import annotations

import unittest
import json

from aiohttp import ClientSession, TCPConnector, web
from aiohttp.resolver import ThreadedResolver
from aiohttp.test_utils import TestServer
from gateway_test_path import add_gateway_component_path

add_gateway_component_path()

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
        self.storage_body = json.dumps({
            "observed_at": "2026-11-13T09:00:00Z",
            "properties": {
                "card_status": {"code": 0, "value": 1, "unit": None},
                "total": {"code": 0, "value": 0, "unit": None},
            },
        })
        self.storage_requests = []

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

        async def storage(request):
            self.storage_requests.append((request.match_info["camera_id"], request.headers.get("Authorization")))
            return web.Response(text=self.storage_body, content_type="application/json")

        async def snapshot(request):
            if request.headers.get("Authorization") != "Bearer test-token":
                return web.Response(status=401)
            return web.Response(body=b"\xff\xd8\x01\xff\xd9", content_type="image/jpeg")

        app = web.Application()
        app.router.add_get("/v1/cameras", inventory)
        app.router.add_get("/v2/cameras", inventory)
        app.router.add_get("/v2/info", info)
        app.router.add_get("/v2/cameras/{camera_id}/settings", settings)
        app.router.add_get("/v2/cameras/{camera_id}/storage", storage)
        app.router.add_get("/v2/cameras/{camera_id}/snapshot", snapshot)
        self.server = TestServer(app)
        await self.server.start_server()
        self.session = ClientSession(connector=TCPConnector(resolver=ThreadedResolver()))
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

    async def test_source_format_requires_active_stream_and_valid_dimensions(self) -> None:
        self.managed_status = 200
        camera = {"id": "test", "name": "Test", "capabilities": {"video": True, "ptz": False}}
        source_format = {"codec": "H265", "width": 1920, "height": 1080}
        for state, height, expected_format in (
            ("streaming", 1080, {"source_codec": "H265", "source_resolution": "1920×1080"}),
            ("streaming", True, {"source_codec": "H265"}),
            ("retrying", 1080, {}),
        ):
            with self.subTest(state=state, height=height):
                camera["video"] = {"state": state, "source_format": {**source_format, "height": height}}
                self.managed_body = json.dumps({"cameras": [camera]})
                result, = await self.client.cameras()
                self.assertEqual({key: value for key, value in result.items() if key.startswith("source_")},
                                 expected_format)

    async def test_managed_camera_without_stream_or_ptz_does_not_invent_features(self) -> None:
        self.managed_status = 200
        self.managed_body = json.dumps({"cameras": [{
            "id": "offline", "name": "Offline", "capabilities": {"video": True, "ptz": False},
            "video": {"state": "offline"}, "streams": [],
        }]})
        cameras = await self.client.cameras()
        self.assertIsNone(cameras[0]["rtsp_url"])
        self.assertFalse(cameras[0]["ptz"])

    async def test_managed_support_metadata_is_optional_and_filters_unknown_fields(self) -> None:
        self.managed_status = 200
        self.managed_body = json.dumps({"cameras": [{
            "id": "c201-test", "name": "C201 test",
            "capabilities": {"video": True, "ptz": False}, "streams": [],
            "support": {
                "schema_version": 1,
                "future_field": "ignored",
                "features": {
                    "video": {
                        "implementation": "implemented", "validation": "offline",
                        "path": "camera_lan", "availability": "available", "requires_cloud": [],
                        "evidence_ref": "offline-run#video",
                    },
                    "unknown_future_feature": {"implementation": "implemented"},
                    "ptz": {"implementation": "invented", "validation": "device", "path": "camera_lan"},
                },
            },
        }]})
        cameras = await self.client.cameras()
        self.assertEqual(cameras[0]["support"], {
            "schema_version": 1,
            "features": {
                "video": {
                    "implementation": "implemented", "validation": "offline",
                    "path": "camera_lan", "availability": "available", "requires_cloud": [], "evidence_ref": "offline-run#video",
                },
            },
        })

    async def test_unknown_support_schema_does_not_break_older_camera_behavior(self) -> None:
        self.managed_status = 200
        self.managed_body = json.dumps({"cameras": [{
            "id": "old-camera", "name": "Old camera",
            "capabilities": {"video": True, "ptz": False}, "streams": [],
            "support": {"schema_version": 2, "features": {"video": {"path": "local"}}},
        }]})
        cameras = await self.client.cameras()
        self.assertEqual(cameras[0]["id"], "old-camera")
        self.assertIsNone(cameras[0]["rtsp_url"])
        self.assertNotIn("support", cameras[0])

    async def test_mixed_inventory_keeps_cameras_with_unknown_or_invalid_optional_support(self) -> None:
        self.managed_status = 200
        self.managed_body = json.dumps({"cameras": [
            {"id": "valid", "name": "Valid", "capabilities": {"video": True, "ptz": False}, "streams": []},
            {"id": "future", "name": "Future", "capabilities": {"video": True, "ptz": False}, "streams": [], "support": {"schema_version": 7, "features": {}}},
            {"id": "partial", "name": "Partial", "capabilities": {"video": True, "ptz": False}, "streams": [], "support": {"schema_version": 1, "features": {"video": {"implementation": "invented"}}}},
        ]})
        cameras = await self.client.cameras()
        self.assertEqual([camera["id"] for camera in cameras], ["valid", "future", "partial"])
        self.assertTrue(all(camera["rtsp_url"] is None and camera["ptz"] is False for camera in cameras))
        self.assertTrue(all("support" not in camera for camera in cameras))

    async def test_support_availability_reason_pairing_is_validated(self) -> None:
        for availability, reason, accepted in (
            (None, "", True),
            ("unknown", "unverified_firmware", True),
            ("available", "", True),
            ("unavailable", "unsupported_model", True),
            ("unknown", "future_reason", False),
            ("available", "timeout", False),
            ("unavailable", "", False),
        ):
            detail = {
                "implementation": "implemented", "validation": "offline", "path": "camera_lan",
                "requires_cloud": [],
            }
            if availability is not None:
                detail["availability"] = availability
            if reason:
                detail["reason"] = reason
            self.managed_status = 200
            self.managed_body = json.dumps({"cameras": [{
                "id": "cam", "name": "Camera", "capabilities": {"video": True, "ptz": False},
                "streams": [], "support": {"schema_version": 1, "features": {"video": detail}},
            }]})
            cameras = await self.client.cameras()
            self.assertEqual("support" in cameras[0], accepted, (availability, reason))
            if accepted:
                self.assertEqual(cameras[0]["support"]["features"]["video"]["availability"], availability or "unknown")

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

    async def test_camera_storage_is_read_only_raw_and_requires_null_units(self) -> None:
        storage = await self.client.camera_storage("c201-test")
        self.assertEqual(storage["properties"]["card_status"], {"code": 0, "value": 1, "unit": None})
        self.assertEqual(storage["properties"]["total"], {"code": 0, "value": 0, "unit": None})
        self.assertEqual(self.storage_requests, [("c201-test", "Bearer test-token")])

        self.storage_body = json.dumps({
            "observed_at": "now",
            "properties": {"total": {"code": 0, "value": 1, "unit": "bytes"}},
        })
        with self.assertRaises(GatewayError):
            await self.client.camera_storage("c201-test")

        self.storage_body = json.dumps({
            "observed_at": "now",
            "properties": {"total": {"code": 0, "value": (1 << 31) + 1, "unit": None}},
        })
        with self.assertRaises(GatewayError):
            await self.client.camera_storage("c201-test")

    async def test_storage_support_flag_is_separate_and_never_reads_without_advertisement(self) -> None:
        self.info_body = '{"features":["camera_selection"]}'
        self.assertFalse(await self.client.supports_camera_storage())
        self.assertEqual(self.storage_requests, [])

        self.info_body = '{"features":["camera_selection","sd_capacity"]}'
        self.assertTrue(await self.client.supports_camera_storage())
        self.assertEqual(self.storage_requests, [])

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

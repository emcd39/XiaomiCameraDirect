"""Provision and exercise the integration in a fresh Home Assistant container."""
from __future__ import annotations

import argparse
import asyncio
import json
import os
import time

import aiohttp
from aiohttp import web

BASE = os.environ.get("HA_URL", "http://127.0.0.1:8123").rstrip("/")
USERNAME = os.environ["HA_USERNAME"]
PASSWORD = os.environ["HA_PASSWORD"]
TOKEN = os.environ["XLG_API_TOKEN"]
GATEWAY_URL = os.environ.get("XLG_GATEWAY_URL", "http://xlg-gateway:8099")
CAMERAS = ("camera.test_garden", "camera.test_porch")
PTZ = {
    "up": "button.test_garden_ptz_up",
    "down": "button.test_garden_ptz_down",
    "left": "button.test_garden_ptz_left",
    "right": "button.test_garden_ptz_right",
}
CLIENT_ID = f"{BASE}/"


async def request(session, method: str, path: str, *, payload=None, token=None, form=None):
    headers = {"Authorization": f"Bearer {token}"} if token else {}
    async with session.request(
        method, f"{BASE}{path}", headers=headers, json=payload, data=form
    ) as response:
        if response.status >= 400:
            raise RuntimeError(f"Home Assistant {method} {path} returned HTTP {response.status}")
        if response.content_type == "application/json":
            return await response.json()
        await response.read()
        return None


async def issue_token(session, auth_code: str) -> str:
    result = await request(
        session,
        "POST",
        "/auth/token",
        form={
            "grant_type": "authorization_code",
            "code": auth_code,
            "client_id": CLIENT_ID,
            "redirect_uri": CLIENT_ID,
        },
    )
    access_token = result.get("access_token") if isinstance(result, dict) else None
    if not isinstance(access_token, str):
        raise RuntimeError("Home Assistant did not issue an access token")
    return access_token


async def bootstrap(session) -> str:
    status = await request(session, "GET", "/api/onboarding")
    steps = {item["step"]: item["done"] for item in status}
    if any(steps.values()):
        raise RuntimeError("The E2E Home Assistant config is not fresh; refusing a stale run")

    user_result = await request(
        session,
        "POST",
        "/api/onboarding/users",
        payload={
            "name": "Xiaomi Gateway E2E",
            "username": USERNAME,
            "password": PASSWORD,
            "client_id": CLIENT_ID,
            "language": "en",
        },
    )
    token = await issue_token(session, user_result["auth_code"])

    await request(session, "POST", "/api/onboarding/core_config", token=token)
    await request(session, "POST", "/api/onboarding/analytics", token=token)
    await request(
        session,
        "POST",
        "/api/onboarding/integration",
        payload={"client_id": CLIENT_ID, "redirect_uri": CLIENT_ID},
        token=token,
    )
    completed = await request(session, "GET", "/api/onboarding")
    if not all(item["done"] for item in completed):
        raise RuntimeError("Home Assistant onboarding did not complete")
    return token


async def api(session, token: str, method: str, path: str, payload=None):
    return await request(session, method, path, payload=payload, token=token)


async def setup_integration(
    session,
    token: str,
    gateway_url: str = GATEWAY_URL,
    api_token: str = TOKEN,
) -> str:
    flow = await api(
        session,
        token,
        "POST",
        "/api/config/config_entries/flow",
        {"handler": "xiaomi_local_gateway", "show_advanced_options": False},
    )
    if not isinstance(flow, dict) or flow.get("type") != "form" or flow.get("step_id") != "user":
        safe = {
            key: flow[key]
            for key in ("type", "step_id", "handler", "errors", "reason", "message")
            if isinstance(flow, dict) and key in flow
        }
        raise RuntimeError("Home Assistant could not initialize the Xiaomi integration flow: " + json.dumps(safe, sort_keys=True))

    result = await api(
        session,
        token,
        "POST",
        f"/api/config/config_entries/flow/{flow['flow_id']}",
        {"url": gateway_url, "token": api_token},
    )
    if not isinstance(result, dict) or result.get("type") != "create_entry":
        errors = result.get("errors", {}) if isinstance(result, dict) else {}
        raise RuntimeError(f"Home Assistant rejected the integration flow: {sorted(errors)}")
    entry = result.get("result")
    entry_id = entry.get("entry_id") if isinstance(entry, dict) else None
    if not isinstance(entry_id, str) or not entry_id:
        raise RuntimeError("Home Assistant did not return the new config-entry ID")
    return entry_id


async def websocket_result(session, token: str, command: dict):
    async with session.ws_connect(f"{BASE}/api/websocket") as websocket:
        hello = await websocket.receive_json()
        if not isinstance(hello, dict) or hello.get("type") != "auth_required":
            raise RuntimeError("Home Assistant WebSocket did not request authentication")
        await websocket.send_json({"type": "auth", "access_token": token})
        auth = await websocket.receive_json()
        if not isinstance(auth, dict) or auth.get("type") != "auth_ok":
            raise RuntimeError("Home Assistant WebSocket authentication failed")
        await websocket.send_json({"id": 1, **command})
        response = await websocket.receive_json()
        if (
            not isinstance(response, dict)
            or response.get("type") != "result"
            or response.get("success") is not True
        ):
            raise RuntimeError("Home Assistant WebSocket registry query failed")
        return response.get("result")


async def wait_for_condition(check, timeout: int = 90):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = await check()
        if result:
            return result
        await asyncio.sleep(0.5)
    raise RuntimeError("Timed out waiting for Home Assistant settings recovery")


async def test_late_settings_capability(session, token: str) -> None:
    api_token = "isolated-settings-token"
    state = {
        "info_calls": 0,
        "settings_calls": 0,
        "feature_enabled": False,
        "properties": {
            "night_shot": {"code": 0, "value": 2},
            "indicator_light": {"code": 0, "value": True},
            "motion_detection": {"code": 0, "value": True},
            "human_detection": {"code": 0, "value": False},
            "move_detection": {"code": 0, "value": True},
            "pet_detection": {"code": 0, "value": False},
        },
    }

    def authorized(request):
        return request.headers.get("Authorization") == f"Bearer {api_token}"

    async def cameras(request):
        if not authorized(request):
            return web.json_response({"error": "unauthorized"}, status=401)
        return web.json_response({"cameras": [{
            "id": "late_c201",
            "name": "Late C201",
            "model": "xiaomi.camera.c201",
            "capabilities": {"video": True, "ptz": True},
            "streams": [{"id": "main", "protocol": "rtsp", "url": "rtsp://127.0.0.1:8554/late-c201"}],
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
                        "evidence_ref": "late-settings-e2e",
                    },
                },
            },
        }]})

    async def info(request):
        if not authorized(request):
            return web.json_response({"error": "unauthorized"}, status=401)
        state["info_calls"] += 1
        if state["info_calls"] == 1:
            return web.json_response({"error": "backend_unavailable"}, status=503)
        features = ["camera_selection"]
        if state["feature_enabled"]:
            features.append("camera_settings_read")
        return web.json_response({"api_version": 2, "features": features})

    async def settings(request):
        if not authorized(request):
            return web.json_response({"error": "unauthorized"}, status=401)
        state["settings_calls"] += 1
        return web.json_response({
            "observed_at": "2026-11-12T12:00:00Z",
            "properties": state["properties"],
        })

    app = web.Application()
    app.router.add_get("/v2/cameras", cameras)
    app.router.add_get("/v2/info", info)
    app.router.add_get("/v2/cameras/{camera_id}/settings", settings)
    runner = web.AppRunner(app)
    await runner.setup()
    site = web.TCPSite(runner, "127.0.0.1", 0)
    await site.start()
    port = site._server.sockets[0].getsockname()[1]
    gateway_url = f"http://127.0.0.1:{port}"

    try:
        entry_id = await setup_integration(session, token, gateway_url, api_token)

        async def controls_loaded():
            current = await api(session, token, "GET", "/api/states")
            camera = next((item for item in current if item["entity_id"] == "camera.late_c201"), None)
            left = next((item for item in current if item["entity_id"] == "button.late_c201_ptz_left"), None)
            if camera and left and camera["state"] != "unavailable" and left["state"] != "unavailable":
                return current, camera, left
            return None

        _, camera_entity, _ = await wait_for_condition(controls_loaded)
        camera_support = camera_entity.get("attributes", {}).get("support")
        expected_camera_support = {
            "schema_version": 1,
            "features": {
                "camera_settings_read": {
                    "implementation": "implemented",
                    "validation": "offline",
                    "path": "cloud",
                    "availability": "unknown",
                    "reason": "unverified_firmware",
                    "requires_cloud": ["account_auth", "inventory", "settings_read"],
                    "evidence_ref": "late-settings-e2e",
                    "reason_text": "Current camera firmware has not been verified for this feature.",
                },
            },
        }
        if camera_support != expected_camera_support:
            raise RuntimeError(f"Camera support diagnostics = {camera_support}; expected {expected_camera_support}")

        async def initial_info_failed():
            return state["info_calls"] >= 1

        await wait_for_condition(initial_info_failed)
        if state["info_calls"] < 1 or state["settings_calls"] != 0:
            raise RuntimeError("Settings were queried before capability was advertised")

        async def entry_is_loaded():
            entries = await api(session, token, "GET", "/api/config/config_entries/entry")
            entry = next((item for item in entries if item.get("entry_id") == entry_id), None)
            return entry if entry and entry.get("state") == "loaded" else None

        loaded_entry = await wait_for_condition(entry_is_loaded)
        if loaded_entry.get("state") != "loaded":
            raise RuntimeError("The C201 config entry was not loaded after the initial info failure")

        # The real config-entry setup has returned and camera/PTZ entities exist.
        state["feature_enabled"] = True

        expected_binary_states = {
            "late_c201_indicator_light_enabled": "on",
            "late_c201_motion_detection_enabled": "on",
            "late_c201_human_detection_enabled": "off",
            "late_c201_move_detection_enabled": "on",
            "late_c201_pet_detection_enabled": "off",
        }
        expected_domains = {
            "late_c201_night_shot_mode": "sensor",
            **{unique_id: "binary_sensor" for unique_id in expected_binary_states},
        }

        async def settings_entities_loaded():
            registry = await websocket_result(session, token, {"type": "config/entity_registry/list"})
            if not isinstance(registry, list):
                raise RuntimeError("Home Assistant returned an invalid entity registry")
            own_entities = {
                item["unique_id"]: item
                for item in registry
                if item.get("config_entry_id") == entry_id
            }
            if any(
                unique_id not in own_entities
                or own_entities[unique_id].get("entity_id", "").split(".", 1)[0] != domain
                for unique_id, domain in expected_domains.items()
            ):
                return None
            current = await api(session, token, "GET", "/api/states")
            current_by_id = {item["entity_id"]: item for item in current}
            by_unique_id = {
                unique_id: current_by_id.get(item["entity_id"])
                for unique_id, item in own_entities.items()
                if unique_id in expected_domains
            }
            night_state = by_unique_id.get("late_c201_night_shot_mode")
            actual_binary_states = {
                unique_id: item.get("state") if item else None
                for unique_id, item in by_unique_id.items()
                if unique_id in expected_binary_states
            }
            if night_state is not None and night_state.get("state") == "Auto" and actual_binary_states == expected_binary_states:
                return own_entities, current_by_id, actual_binary_states
            return None

        loaded_entities = await wait_for_condition(settings_entities_loaded, timeout=90)
        own_entities, current_by_id, actual_binary_states = loaded_entities
        if state["settings_calls"] < 1:
            raise RuntimeError("Capability appeared but the settings endpoint was not called")
        night_entry = own_entities["late_c201_night_shot_mode"]
        night = current_by_id.get(night_entry["entity_id"])
        if night is None or night.get("state") != "Auto":
            raise RuntimeError("Night Shot did not report Auto for this config entry")
        if actual_binary_states != expected_binary_states:
            raise RuntimeError(f"C201 binary settings = {actual_binary_states}; expected {expected_binary_states}")

        state["properties"]["night_shot"] = {"code": -704040003, "value": None}
        await api(
            session,
            token,
            "POST",
            "/api/services/homeassistant/update_entity",
            {"entity_id": night["entity_id"]},
        )

        async def night_unavailable():
            current_state = await api(session, token, "GET", f"/api/states/{night['entity_id']}")
            return current_state if current_state.get("state") == "unavailable" else None

        await wait_for_condition(night_unavailable)
        if state["settings_calls"] < 2:
            raise RuntimeError("Manual entity refresh did not request current settings")

        print(json.dumps({
            "phase": "late-settings",
            "loaded_camera_and_ptz": True,
            "support_diagnostics": "versioned_and_cloud_dependency_visible",
            "config_entry_state_before_feature": loaded_entry["state"],
            "info_requests": state["info_calls"],
            "settings_requests": state["settings_calls"],
            "night_shot_initial_state": night["state"],
            "binary_sensor_states": actual_binary_states,
            "night_shot_after_error": "unavailable",
            "result": "ok",
        }, sort_keys=True))
    finally:
        await runner.cleanup()


async def states(session, token: str) -> dict[str, str]:
    response = await api(session, token, "GET", "/api/states")
    by_id = {item["entity_id"]: item["state"] for item in response}
    missing = [entity for entity in (*CAMERAS, *PTZ.values()) if entity not in by_id]
    if missing:
        raise RuntimeError(f"Expected test entities are missing: {', '.join(missing)}")
    return by_id


async def verify_camera_features(session, token: str) -> dict[str, object]:
    cameras = {}
    for entity_id in CAMERAS:
        state = await api(session, token, "GET", f"/api/states/{entity_id}")
        features = state.get("attributes", {}).get("supported_features", 0)
        if not isinstance(features, int) or features <= 0:
            raise RuntimeError(f"Camera stream source is not advertised for {entity_id}")
        cameras[entity_id] = features

    all_states = await api(session, token, "GET", "/api/states")
    entity_ids = {item["entity_id"] for item in all_states}
    unsupported_ptz = [
        f"button.test_porch_ptz_{direction}"
        for direction in PTZ
        if f"button.test_porch_ptz_{direction}" in entity_ids
    ]
    if unsupported_ptz:
        raise RuntimeError("PTZ controls appeared for a camera that does not advertise PTZ")
    return {"camera_stream_features": cameras, "unsupported_ptz_entities": unsupported_ptz}


async def wait_for_state(session, token: str, expected: str) -> dict[str, str]:
    deadline = time.monotonic() + 90
    last = {}
    while time.monotonic() < deadline:
        last = await states(session, token)
        if all(last[camera] == expected for camera in CAMERAS):
            return last
        await asyncio.sleep(2)
    raise RuntimeError(f"Camera states did not reach {expected!r}")


async def run(mode: str) -> None:
    timeout = aiohttp.ClientTimeout(total=20)
    async with aiohttp.ClientSession(timeout=timeout) as session:
        token = None
        if mode == "setup":
            deadline = time.monotonic() + 120
            while True:
                try:
                    token = await bootstrap(session)
                    break
                except aiohttp.ClientConnectorError:
                    if time.monotonic() >= deadline:
                        raise RuntimeError("Fresh Home Assistant did not start within 120 seconds")
                    await asyncio.sleep(2)
            await setup_integration(session, token)
            result = await wait_for_state(session, token, "idle")
            features = await verify_camera_features(session, token)
            print(json.dumps({"phase": mode, "camera_states": {key: result[key] for key in CAMERAS}, **features, "result": "ok"}, sort_keys=True))
            return

        # The bootstrap account is local to this disposable HA config.
        flow_result = await request(
            session,
            "POST",
            "/auth/login_flow",
            payload={"client_id": CLIENT_ID, "handler": ["homeassistant", None], "redirect_uri": CLIENT_ID},
        )
        completed = await request(
            session,
            "POST",
            f"/auth/login_flow/{flow_result['flow_id']}",
            payload={"username": USERNAME, "password": PASSWORD, "client_id": CLIENT_ID},
        )
        login_result = completed.get("result") if isinstance(completed, dict) else None
        auth_code = login_result if isinstance(login_result, str) else (
            login_result.get("auth_code") if isinstance(login_result, dict) else None
        )
        if not isinstance(auth_code, str):
            raise RuntimeError("Home Assistant login flow did not return an authorization code")
        token = await issue_token(session, auth_code)

        if mode == "verify-entities":
            current = await states(session, token)
            features = await verify_camera_features(session, token)
            print(json.dumps({"phase": mode, "entities": sorted([*CAMERAS, *PTZ.values()]), **features, "result": "ok"}, sort_keys=True))
            return
        if mode == "press":
            for entity in PTZ.values():
                await api(session, token, "POST", "/api/services/button/press", {"entity_id": entity})
            print(json.dumps({"phase": mode, "requests": len(PTZ), "result": "ok"}, sort_keys=True))
            return
        if mode == "late-settings":
            await test_late_settings_capability(session, token)
            return

        expected = "unavailable" if mode == "wait-unavailable" else "idle"
        current = await wait_for_state(session, token, expected)
        print(json.dumps({"phase": mode, "camera_states": {key: current[key] for key in CAMERAS}, "result": "ok"}, sort_keys=True))


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("mode", choices=("setup", "verify-entities", "press", "wait-unavailable", "wait-available", "late-settings"))
    asyncio.run(run(parser.parse_args().mode))

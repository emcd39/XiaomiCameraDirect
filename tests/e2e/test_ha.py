"""Provision and exercise the integration in a fresh Home Assistant container."""
from __future__ import annotations

import argparse
import asyncio
import json
import os
import time

import aiohttp

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


async def setup_integration(session, token: str) -> None:
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
        {"url": GATEWAY_URL, "token": TOKEN},
    )
    if not isinstance(result, dict) or result.get("type") != "create_entry":
        errors = result.get("errors", {}) if isinstance(result, dict) else {}
        raise RuntimeError(f"Home Assistant rejected the integration flow: {sorted(errors)}")


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

        expected = "unavailable" if mode == "wait-unavailable" else "idle"
        current = await wait_for_state(session, token, expected)
        print(json.dumps({"phase": mode, "camera_states": {key: current[key] for key in CAMERAS}, "result": "ok"}, sort_keys=True))


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("mode", choices=("setup", "verify-entities", "press", "wait-unavailable", "wait-available"))
    asyncio.run(run(parser.parse_args().mode))

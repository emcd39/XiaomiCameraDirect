"""Test the official Frigate motion entities and automations against Mosquitto."""
from __future__ import annotations

import json
import os
import time
from pathlib import Path
from urllib.parse import urlencode
from urllib.error import HTTPError
from urllib.request import Request, urlopen

import paho.mqtt.client as mqtt
import yaml


HA_URL = os.environ.get("HA_URL", "http://127.0.0.1:8123").rstrip("/")
HA_USERNAME = os.environ["HA_USERNAME"]
HA_PASSWORD = os.environ["HA_PASSWORD"]
CLIENT_ID = f"{HA_URL}/"
GATEWAY_URL = os.environ.get("XLG_GATEWAY_URL", "http://xlg-gateway:8099").rstrip("/")
GATEWAY_TOKEN = os.environ["XLG_API_TOKEN"]
MQTT_HOST = os.environ.get("XLG_MQTT_HOST", "xlg-mqtt")
MQTT_PORT = int(os.environ.get("XLG_MQTT_PORT", "1883"))
FRIGATE_URL = os.environ.get("XLG_FRIGATE_URL", "http://xlg-onvif-mock:5000")
MOTION_COUNTERS = {
    "test_garden": "counter.frigate_test_garden_triggers",
    "test_porch": "counter.frigate_test_porch_triggers",
}
FRIGATE_SOURCE_MARKER = Path("/config/custom_components/frigate/.xlg-test-source")
EXPECTED_FRIGATE_SOURCE = "v5.15.6 3f1469c"


def http_json(url: str, method: str = "GET", payload=None, token: str | None = None):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    body = json.dumps(payload).encode("utf-8") if payload is not None else None
    request = Request(url, data=body, headers=headers, method=method)
    try:
        with urlopen(request, timeout=15) as response:
            raw = response.read()
            return json.loads(raw) if raw and response.headers.get_content_type() == "application/json" else None
    except HTTPError as err:
        detail = err.read().decode("utf-8", "replace")
        raise RuntimeError(f"HTTP {err.code} {method} {url}: {detail[:300]}") from err


def ha_api(token: str, method: str, path: str, payload=None):
    return http_json(f"{HA_URL}{path}", method, payload, token)


def issue_token(auth_code: str) -> str:
    request = Request(
        f"{HA_URL}/auth/token",
        data=urlencode({
            "grant_type": "authorization_code",
            "code": auth_code,
            "client_id": CLIENT_ID,
            "redirect_uri": CLIENT_ID,
        }).encode("utf-8"),
        headers={"Content-Type": "application/x-www-form-urlencoded"},
        method="POST",
    )
    with urlopen(request, timeout=15) as response:
        result = json.loads(response.read())
    token = result.get("access_token") if isinstance(result, dict) else None
    if not isinstance(token, str):
        raise RuntimeError("Home Assistant did not issue an access token")
    return token


def login_home_assistant() -> str:
    flow = http_json(
        f"{HA_URL}/auth/login_flow",
        "POST",
        {"client_id": CLIENT_ID, "handler": ["homeassistant", None], "redirect_uri": CLIENT_ID},
    )
    flow_id = flow.get("flow_id") if isinstance(flow, dict) else None
    if not isinstance(flow_id, str):
        raise RuntimeError("Home Assistant did not start the test user login flow")
    result = http_json(
        f"{HA_URL}/auth/login_flow/{flow_id}",
        "POST",
        {"username": HA_USERNAME, "password": HA_PASSWORD, "client_id": CLIENT_ID},
    )
    auth_result = result.get("result") if isinstance(result, dict) else None
    auth_code = auth_result if isinstance(auth_result, str) else (
        auth_result.get("auth_code") if isinstance(auth_result, dict) else None
    )
    if not isinstance(auth_code, str):
        raise RuntimeError("Home Assistant login flow did not return an authorization code")
    return issue_token(auth_code)


def generated_camera_ids() -> list[str]:
    request = Request(
        f"{GATEWAY_URL}/v1/frigate/config",
        headers={"Authorization": f"Bearer {GATEWAY_TOKEN}"},
    )
    with urlopen(request, timeout=5) as response:
        config = yaml.safe_load(response.read().decode("utf-8"))
    cameras = config.get("cameras") if isinstance(config, dict) else None
    if not isinstance(cameras, dict) or not cameras:
        raise RuntimeError("Generated Frigate configuration has no cameras")
    camera_ids = sorted(cameras)
    if set(camera_ids) != set(MOTION_COUNTERS):
        raise RuntimeError(f"Generated cameras {camera_ids} do not match isolated Frigate fixture")
    return camera_ids


def setup_frigate_entry(token: str) -> str:
    flow = ha_api(token, "POST", "/api/config/config_entries/flow", {"handler": "frigate"})
    if not isinstance(flow, dict) or flow.get("type") != "form" or flow.get("step_id") != "user":
        raise RuntimeError(f"Official Frigate config flow did not open: {flow!r}")
    result = ha_api(
        token,
        "POST",
        f"/api/config/config_entries/flow/{flow['flow_id']}",
        {"url": FRIGATE_URL, "validate_ssl": False},
    )
    if not isinstance(result, dict) or result.get("type") != "create_entry":
        raise RuntimeError(f"Home Assistant rejected the official Frigate flow: {result!r}")
    entry = result.get("result")
    entry_id = entry.get("entry_id") if isinstance(entry, dict) else None
    if not isinstance(entry_id, str):
        raise RuntimeError("Home Assistant did not return a Frigate config entry ID")
    return entry_id


def setup_mqtt_entry(token: str) -> str:
    flow = ha_api(token, "POST", "/api/config/config_entries/flow", {"handler": "mqtt"})
    if not isinstance(flow, dict) or flow.get("type") != "form" or flow.get("step_id") != "broker":
        raise RuntimeError(f"Home Assistant MQTT config flow did not open: {flow!r}")
    result = ha_api(
        token,
        "POST",
        f"/api/config/config_entries/flow/{flow['flow_id']}",
        {
            "broker": MQTT_HOST,
            "port": MQTT_PORT,
            "protocol": "5",
            "other_settings": {
                "set_client_cert": False,
                "set_ca_cert": "off",
                "transport": "tcp",
            },
        },
    )
    if not isinstance(result, dict) or result.get("type") != "create_entry":
        raise RuntimeError(f"Home Assistant could not configure the isolated MQTT broker: {result!r}")
    entry = result.get("result")
    entry_id = entry.get("entry_id") if isinstance(entry, dict) else None
    if not isinstance(entry_id, str):
        raise RuntimeError("Home Assistant did not return an MQTT config entry ID")
    return entry_id


def verify_pinned_integration_source() -> str:
    try:
        source = FRIGATE_SOURCE_MARKER.read_text(encoding="utf-8").strip()
    except OSError as err:
        raise RuntimeError("Pinned official Frigate integration source marker is missing") from err
    if source != EXPECTED_FRIGATE_SOURCE:
        raise RuntimeError(f"Unexpected official Frigate integration source: {source!r}")
    return source


def state(token: str, entity_id: str) -> str:
    result = ha_api(token, "GET", f"/api/states/{entity_id}")
    return result.get("state") if isinstance(result, dict) else "missing"


def wait_state(token: str, entity_id: str, expected: str, timeout: float = 20) -> None:
    deadline = time.monotonic() + timeout
    last = "missing"
    while time.monotonic() < deadline:
        try:
            last = state(token, entity_id)
        except RuntimeError:
            last = "missing"
        if last == expected:
            return
        time.sleep(0.2)
    raise RuntimeError(f"{entity_id} did not reach {expected!r}; last state was {last!r}")


def counter(token: str, entity_id: str) -> int:
    current = state(token, entity_id)
    try:
        return int(current)
    except (TypeError, ValueError) as err:
        raise RuntimeError(f"Automation counter {entity_id} is not numeric: {current!r}") from err


def publish(client: mqtt.Client, topic: str, payload: str, *, retain: bool = False) -> None:
    result = client.publish(topic, payload, qos=0, retain=retain)
    result.wait_for_publish(timeout=5)
    if not result.is_published():
        raise RuntimeError(f"Timed out publishing MQTT topic {topic}")


def main() -> None:
    frigate_source = verify_pinned_integration_source()
    camera_ids = generated_camera_ids()
    token = login_home_assistant()
    mqtt_entry_id = setup_mqtt_entry(token)
    entry_id = setup_frigate_entry(token)
    motion_entities = {camera_id: f"binary_sensor.{camera_id}_motion" for camera_id in camera_ids}

    for camera_id, entity_id in motion_entities.items():
        wait_state(token, entity_id, "unavailable")
        expected_counter = MOTION_COUNTERS[camera_id]
        if counter(token, expected_counter) != 0:
            raise RuntimeError(f"Fresh HA automation counter for {camera_id} was not zero")

    client = mqtt.Client(
        callback_api_version=mqtt.CallbackAPIVersion.VERSION2,
        client_id=f"xlg-frigate-motion-test-{os.getpid()}",
    )
    client.connect(MQTT_HOST, MQTT_PORT, keepalive=15)
    client.loop_start()
    try:
        # The official component subscribes to Frigate's global availability
        # topic and each camera's motion topic.
        publish(client, "frigate/available", "online", retain=True)
        for entity_id in motion_entities.values():
            wait_state(token, entity_id, "off")

        garden_entity = motion_entities["test_garden"]
        porch_entity = motion_entities["test_porch"]
        garden_counter = MOTION_COUNTERS["test_garden"]
        porch_counter = MOTION_COUNTERS["test_porch"]

        publish(client, "frigate/test_garden/motion", "ON")
        wait_state(token, garden_entity, "on")
        deadline = time.monotonic() + 10
        while counter(token, garden_counter) != 1 and time.monotonic() < deadline:
            time.sleep(0.2)
        if counter(token, garden_counter) != 1 or counter(token, porch_counter) != 0:
            raise RuntimeError("Garden motion did not trigger exactly its own HA automation once")

        publish(client, "frigate/test_garden/motion", "ON")
        time.sleep(0.5)
        if counter(token, garden_counter) != 1:
            raise RuntimeError("Repeated ON for an unchanged motion state retriggered the garden automation")

        publish(client, "frigate/test_porch/motion", "ON")
        wait_state(token, porch_entity, "on")
        deadline = time.monotonic() + 10
        while counter(token, porch_counter) != 1 and time.monotonic() < deadline:
            time.sleep(0.2)
        if counter(token, porch_counter) != 1 or counter(token, garden_counter) != 1:
            raise RuntimeError("Porch motion was not isolated to the porch automation")

        publish(client, "frigate/available", "offline", retain=True)
        for entity_id in motion_entities.values():
            wait_state(token, entity_id, "unavailable")
        if counter(token, garden_counter) != 1 or counter(token, porch_counter) != 1:
            raise RuntimeError("Frigate source outage unexpectedly triggered a motion automation")

        # Frigate reports the current camera states around startup. An OFF
        # snapshot clears any pre-outage ON state before publishing available.
        publish(client, "frigate/test_garden/motion", "OFF")
        publish(client, "frigate/test_porch/motion", "OFF")
        publish(client, "frigate/available", "online", retain=True)
        for entity_id in motion_entities.values():
            wait_state(token, entity_id, "off")

        publish(client, "frigate/test_garden/motion", "ON")
        wait_state(token, garden_entity, "on")
        deadline = time.monotonic() + 10
        while counter(token, garden_counter) != 2 and time.monotonic() < deadline:
            time.sleep(0.2)
        if counter(token, garden_counter) != 2 or counter(token, porch_counter) != 1:
            raise RuntimeError("A post-recovery garden event did not trigger exactly one automation")

        print(json.dumps({
            "phase": "frigate_motion_automation",
            "frigate_config_entry": entry_id,
            "mqtt_config_entry": mqtt_entry_id,
            "integration_source": frigate_source,
            "camera_ids_from_generated_config": camera_ids,
            "motion_entities": motion_entities,
            "automation_counts": {"test_garden": counter(token, garden_counter), "test_porch": counter(token, porch_counter)},
            "source_outage_states": {camera_id: "unavailable" for camera_id in camera_ids},
            "result": "ok",
        }, sort_keys=True), flush=True)
    finally:
        publish(client, "frigate/available", "offline", retain=True)
        client.disconnect()
        client.loop_stop()


if __name__ == "__main__":
    main()

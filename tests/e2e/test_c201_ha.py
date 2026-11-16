"""Run private C201 checks through the real HA integration and RTSP path."""
from __future__ import annotations

import argparse
import asyncio
import json
import os
import shutil
import subprocess
import tempfile
import time
from urllib.parse import quote, urlsplit

import aiohttp

from test_ha import (
    BASE,
    CLIENT_ID,
    GATEWAY_URL,
    PASSWORD,
    TOKEN,
    USERNAME,
    api,
    bootstrap,
    setup_integration,
)

TOKEN_CACHE = "/config/.xlg-c201-e2e-token.json"
RESULT_CACHE = "/config/.xlg-c201-e2e-result.json"
_stream_monotonic = time.monotonic


def report_result(payload: dict[str, object]) -> None:
    encoded = json.dumps(payload, sort_keys=True)
    descriptor, temporary_path = tempfile.mkstemp(prefix=".xlg-c201-result-", dir="/config")
    try:
        try:
            os.fchmod(descriptor, 0o600)
        except (AttributeError, OSError):
            pass
        with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
            handle.write(encoded)
            handle.write("\n")
        os.replace(temporary_path, RESULT_CACHE)
        try:
            os.chmod(RESULT_CACHE, 0o600)
        except OSError:
            pass
    finally:
        if os.path.exists(temporary_path):
            os.unlink(temporary_path)
    print(encoded, flush=True)


def save_cached_token(access_token: str) -> None:
    payload = json.dumps({"access_token": access_token})
    descriptor, temporary_path = tempfile.mkstemp(prefix=".xlg-c201-token-", dir="/config")
    try:
        try:
            os.fchmod(descriptor, 0o600)
        except (AttributeError, OSError):
            pass
        with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
            handle.write(payload)
            handle.write("\n")
        os.replace(temporary_path, TOKEN_CACHE)
        try:
            os.chmod(TOKEN_CACHE, 0o600)
        except OSError:
            pass
    finally:
        if os.path.exists(temporary_path):
            os.unlink(temporary_path)


def load_cached_token() -> str | None:
    try:
        with open(TOKEN_CACHE, encoding="utf-8") as handle:
            cached = json.load(handle)
    except (OSError, json.JSONDecodeError):
        return None
    token = cached.get("access_token") if isinstance(cached, dict) else None
    return token if isinstance(token, str) and token else None


async def issue_token_and_cache(session: aiohttp.ClientSession, auth_code: str) -> str:
    result = await test_request(
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
    save_cached_token(access_token)
    return access_token


async def cached_or_login(session: aiohttp.ClientSession) -> str:
    token = load_cached_token()
    return token if token is not None else await login(session)


async def login(session: aiohttp.ClientSession) -> str:
    flow_result = await test_request(
        session,
        "POST",
        "/auth/login_flow",
        payload={"client_id": CLIENT_ID, "handler": ["homeassistant", None], "redirect_uri": CLIENT_ID},
    )
    completed = await test_request(
        session,
        "POST",
        f"/auth/login_flow/{flow_result['flow_id']}",
        payload={"username": USERNAME, "password": PASSWORD, "client_id": CLIENT_ID},
    )
    result = completed.get("result") if isinstance(completed, dict) else None
    auth_code = result if isinstance(result, str) else (result.get("auth_code") if isinstance(result, dict) else None)
    if not isinstance(auth_code, str):
        flow_type = completed.get("type") if isinstance(completed, dict) else None
        step_id = completed.get("step_id") if isinstance(completed, dict) else None
        errors = completed.get("errors", {}) if isinstance(completed, dict) else {}
        base_error = errors.get("base") if isinstance(errors, dict) else None
        error_labels = {
            "invalid_auth": "invalid_test_account",
            "invalid_code": "invalid_verification_code",
            "invalid_client": "invalid_local_client",
            "unknown": "unknown_auth_error",
        }
        safe_result = {
            "phase": "ha_login",
            "flow_type": flow_type if flow_type in {"form", "abort", "create_entry", "external", "progress"} else "other",
            "step_id": step_id if step_id in {"user", "mfa", "init", "totp", "select_provider"} else None,
            "error": error_labels.get(base_error, "other"),
        }
        print(json.dumps(safe_result, sort_keys=True))
        raise RuntimeError("Home Assistant local test login did not complete")
    return await issue_token_and_cache(session, auth_code)


async def test_request(session, method: str, path: str, *, payload=None, token=None, form=None):
    headers = {"Authorization": f"Bearer {token}"} if token else {}
    async with session.request(method, f"{BASE}{path}", headers=headers, json=payload, data=form) as response:
        if response.status >= 400:
            raise RuntimeError(f"Home Assistant {method} {path} returned HTTP {response.status}")
        if response.content_type == "application/json":
            return await response.json()
        return await response.read()


async def gateway_preflight(session: aiohttp.ClientSession) -> None:
    async with session.get(GATEWAY_URL.rstrip("/") + "/healthz", timeout=aiohttp.ClientTimeout(total=10)) as response:
        health_status = response.status
        await response.read()
    if health_status != 200:
        raise RuntimeError(f"Linux gateway health check returned HTTP {health_status}")

    async with session.get(
        GATEWAY_URL.rstrip("/") + "/v2/cameras?include_streams=false",
        headers={"Authorization": f"Bearer {TOKEN}"},
        timeout=aiohttp.ClientTimeout(total=45),
    ) as response:
        inventory_status = response.status
        if inventory_status != 200:
            await response.read()
            raise RuntimeError(f"Linux gateway camera inventory returned HTTP {inventory_status}")
        body = await response.json()
    cameras = body.get("cameras") if isinstance(body, dict) else None
    if not isinstance(cameras, list):
        raise RuntimeError("Linux gateway returned an invalid camera inventory")
    c201_count = sum(1 for camera in cameras if isinstance(camera, dict) and camera.get("model_id") == "xiaomi.camera.c201")
    if c201_count != 1:
        raise RuntimeError("Linux gateway inventory did not contain exactly one C201")
    report_result({
        "phase": "gateway_preflight",
        "health_http": health_status,
        "inventory_http": inventory_status,
        "camera_count": len(cameras),
        "c201_count": c201_count,
        "result": "ok",
    })


async def c201_camera_entity_ids(session: aiohttp.ClientSession, token: str) -> list[str]:
    states = await api(session, token, "GET", "/api/states")
    selected = []
    for item in states:
        entity_id = item.get("entity_id")
        attributes = item.get("attributes", {})
        features = attributes.get("supported_features", 0)
        if (
            isinstance(entity_id, str)
            and entity_id.startswith("camera.")
            and isinstance(features, int)
            and features > 0
            and attributes.get("model_id") == "xiaomi.camera.c201"
        ):
            selected.append(entity_id)
    return selected


async def wait_c201_camera(session: aiohttp.ClientSession, token: str) -> str:
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        cameras = await c201_camera_entity_ids(session, token)
        if len(cameras) == 1:
            return cameras[0]
        if len(cameras) > 1:
            raise RuntimeError("Home Assistant exposed more than one C201 camera entity")
        await asyncio.sleep(2)
    raise RuntimeError("Home Assistant did not create the xiaomi.camera.c201 entity")


async def verify_snapshot(session: aiohttp.ClientSession, token: str, entity_id: str) -> int:
    headers = {"Authorization": f"Bearer {token}"}
    async with session.get(f"{BASE}/api/camera_proxy/{quote(entity_id, safe='')}", headers=headers) as response:
        if response.status != 200:
            await response.read()
            raise RuntimeError(f"Home Assistant C201 camera proxy returned HTTP {response.status}")
        if response.content_type != "image/jpeg":
            await response.read()
            raise RuntimeError("Home Assistant C201 camera proxy returned an unexpected content type")
        data = bytearray()
        chunks = 0
        while True:
            remaining = 8 * 1024 * 1024 + 1 - len(data)
            chunk = await response.content.read(min(256, remaining))
            if not chunk:
                break
            data.extend(chunk)
            chunks += 1
            if len(data) > 8 * 1024 * 1024:
                raise RuntimeError("Home Assistant C201 JPEG exceeded the 8 MiB limit")
    if not data.startswith(b"\xff\xd8") or not data.endswith(b"\xff\xd9"):
        raise RuntimeError("Home Assistant C201 camera proxy did not return JPEG framing")
    executable = shutil.which("ffmpeg")
    if not executable:
        raise RuntimeError("FFmpeg is not available in the Home Assistant container")
    decoded = await asyncio.to_thread(
        subprocess.run,
        [
            executable, "-nostdin", "-hide_banner", "-loglevel", "error",
            "-f", "image2pipe", "-c:v", "mjpeg", "-i", "pipe:0",
            "-frames:v", "1", "-progress", "pipe:1", "-f", "null", "-",
        ],
        input=bytes(data),
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        timeout=20,
        check=False,
    )
    decoded_progress = {}
    for line in decoded.stdout.decode("ascii", errors="ignore").splitlines():
        if "=" in line:
            key, value = line.split("=", 1)
            if key == "frame" and value.isdigit():
                decoded_progress[key] = int(value)
    if decoded.returncode != 0 or decoded_progress.get("frame", 0) < 1:
        raise RuntimeError("Home Assistant C201 camera proxy JPEG could not be decoded by FFmpeg")
    if len(data) > 256 and chunks < 2:
        raise RuntimeError("Home Assistant C201 JPEG chunked-read regression was not exercised")
    return len(data)


async def camera_stream_url(session: aiohttp.ClientSession, token: str, entity_id: str) -> str:
    ws_base = BASE.replace("https://", "wss://", 1) if BASE.startswith("https://") else BASE.replace("http://", "ws://", 1)
    async with session.ws_connect(f"{ws_base}/api/websocket", heartbeat=30) as websocket:
        greeting = await websocket.receive_json(timeout=15)
        if not isinstance(greeting, dict) or greeting.get("type") != "auth_required":
            raise RuntimeError("Home Assistant websocket did not request authentication")
        await websocket.send_json({"type": "auth", "access_token": token})
        authenticated = await websocket.receive_json(timeout=15)
        if not isinstance(authenticated, dict) or authenticated.get("type") != "auth_ok":
            raise RuntimeError("Home Assistant websocket authentication failed")
        await websocket.send_json({
            "id": 1,
            "type": "camera/stream",
            "entity_id": entity_id,
            "format": "hls",
        })
        while True:
            message = await websocket.receive_json(timeout=45)
            if not isinstance(message, dict) or message.get("id") != 1:
                continue
            if not message.get("success"):
                error = message.get("error", {})
                code = error.get("code", "unknown") if isinstance(error, dict) else "unknown"
                raise RuntimeError(f"Home Assistant could not start the C201 stream ({code})")
            result = message.get("result")
            stream_url = result.get("url") if isinstance(result, dict) else None
            parsed = urlsplit(stream_url or "")
            if not parsed.path.startswith("/api/hls/") or parsed.username or parsed.password:
                raise RuntimeError("Home Assistant returned an unexpected stream endpoint")
            # Use only the signed HLS path from HA and pin the transport back to
            # this isolated HA instance, regardless of the URL's advertised host.
            query = f"?{parsed.query}" if parsed.query else ""
            return f"{BASE}{parsed.path}{query}"


async def stream_seconds(
    session: aiohttp.ClientSession, token: str, entity_id: str, seconds: int
) -> dict[str, int | str | list[str] | None]:
    started_at = _stream_monotonic()
    returncode: int | None = None
    stdout: bytes | str = b""
    failures: list[str] = []
    timed_out = False
    executable = shutil.which("ffmpeg")
    if not executable:
        failures.append("ffmpeg_unavailable")
    else:
        try:
            stream_url = await camera_stream_url(session, token, entity_id)
            command = [
                executable, "-nostdin", "-hide_banner", "-loglevel", "error",
                "-i", stream_url, "-t", str(seconds), "-an", "-progress", "pipe:1", "-f", "null", "-",
            ]
            result = await asyncio.to_thread(
                subprocess.run,
                command,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE,
                stderr=subprocess.DEVNULL,
                timeout=seconds + 60,
                check=False,
            )
            returncode = result.returncode
            stdout = result.stdout or b""
        except subprocess.TimeoutExpired as error:
            timed_out = True
            stdout = error.stdout or b""
        except Exception:
            # Exception text may contain the signed HLS URL; emit only a safe reason code.
            failures.append("hls_source_or_ffmpeg_error")
    wall_seconds = _stream_monotonic() - started_at
    if isinstance(stdout, str):
        stdout = stdout.encode("ascii", errors="ignore")
    progress = {}
    for line in stdout.decode("ascii", errors="ignore").splitlines():
        if "=" in line:
            key, value = line.split("=", 1)
            if key in {"frame", "out_time_ms"} and value.isdigit():
                progress[key] = int(value)
    decoded_seconds = progress.get("out_time_ms", 0) / 1_000_000
    frame_count = progress.get("frame", 0)
    if timed_out:
        failures.append("ffmpeg_timeout")
    elif returncode is not None and returncode != 0:
        failures.append("ffmpeg_nonzero_exit")
    if returncode == 0 and frame_count < 1:
        failures.append("no_decoded_frames")
    if decoded_seconds < max(1, seconds - 2):
        failures.append("decoded_duration_below_threshold")
    if wall_seconds < max(1, seconds - 5):
        failures.append("wall_duration_below_threshold")
    return {
        "entity_id": entity_id,
        "requested_seconds": seconds,
        "frame": frame_count,
        "decoded_seconds": int(decoded_seconds),
        "wall_seconds": int(wall_seconds),
        "ffmpeg_returncode": returncode,
        "result": "failed" if failures else "ok",
        "failures": failures,
    }


async def direct_rtsp_seconds(session: aiohttp.ClientSession, seconds: int) -> dict[str, int]:
    executable = shutil.which("ffmpeg")
    if not executable:
        raise RuntimeError("FFmpeg is not available in the Home Assistant container")
    body = None
    for inventory_path in ("/v2/cameras?include_streams=false", "/v1/cameras"):
        async with session.get(
            GATEWAY_URL.rstrip("/") + inventory_path,
            headers={"Authorization": f"Bearer {TOKEN}"},
            timeout=aiohttp.ClientTimeout(total=45),
        ) as response:
            if response.status == 404:
                continue
            if response.status == 503:
                try:
                    error_body = await response.json(content_type=None)
                except (aiohttp.ContentTypeError, json.JSONDecodeError):
                    error_body = None
                if isinstance(error_body, dict) and error_body.get("error") == "backend_unavailable":
                    continue
            if response.status != 200:
                raise RuntimeError(f"Gateway camera inventory returned HTTP {response.status}")
            body = await response.json()
            break
    cameras = body.get("cameras") if isinstance(body, dict) else None
    if not isinstance(cameras, list):
        raise RuntimeError("Gateway returned an invalid managed camera inventory")
    matching = [camera for camera in cameras
                if camera.get("model_id", camera.get("model")) == "xiaomi.camera.c201"]
    if len(matching) != 1 or not isinstance(matching[0].get("id"), str):
        raise RuntimeError("Gateway inventory did not identify exactly one C201")
    camera = matching[0]
    ready_source = body.get("on_demand_streams") is True
    if ready_source:
        path = f"/v2/cameras/{quote(camera['id'], safe='')}/stream"
    elif inventory_path.startswith("/v2/"):
        path = f"/v2/cameras?streams_for={quote(camera['id'], safe='')}"
    else:
        path = None
    if path is not None:
        async with session.get(
            GATEWAY_URL.rstrip("/") + path,
            headers={"Authorization": f"Bearer {TOKEN}"},
            timeout=aiohttp.ClientTimeout(total=24),
        ) as response:
            if response.status != 200:
                raise RuntimeError(f"C201 gateway source request returned HTTP {response.status}")
            result = await response.json()
        if ready_source:
            camera = result
        else:
            camera = next((row for row in result.get("cameras", []) if row.get("id") == camera["id"]), {})
    sources = [stream["url"] for stream in camera.get("streams", [])
               if stream.get("protocol") == "rtsp" and isinstance(stream.get("url"), str)]
    if isinstance(camera.get("rtsp_url"), str):
        sources.append(camera["rtsp_url"])
    if len(sources) != 1:
        raise RuntimeError("Gateway did not provide exactly one C201 RTSP source")
    source = sources[0]
    parsed = urlsplit(source or "")
    if parsed.scheme != "rtsp" or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment:
        raise RuntimeError("Gateway did not provide a valid credential-free C201 RTSP source")

    command = [
        executable, "-nostdin", "-hide_banner", "-loglevel", "error", "-rtsp_transport", "tcp",
        "-i", source, "-t", str(seconds), "-an", "-progress", "pipe:1", "-f", "null", "-",
    ]
    deadline = time.monotonic() + 55
    attempts = 0
    while time.monotonic() < deadline:
        attempts += 1
        started_at = time.monotonic()
        try:
            result = await asyncio.to_thread(
                subprocess.run,
                command,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE,
                stderr=subprocess.DEVNULL,
                timeout=min(seconds + 15, 25),
                check=False,
            )
        except subprocess.TimeoutExpired:
            if ready_source:
                break
            await asyncio.sleep(1)
            continue
        wall_seconds = time.monotonic() - started_at
        progress = {}
        for line in result.stdout.decode("ascii", errors="ignore").splitlines():
            if "=" in line:
                key, value = line.split("=", 1)
                if key in {"frame", "out_time_ms"} and value.isdigit():
                    progress[key] = int(value)
        decoded_seconds = progress.get("out_time_ms", 0) / 1_000_000
        if (
            result.returncode == 0
            and progress.get("frame", 0) >= 1
            and decoded_seconds >= max(1, seconds - 2)
            and wall_seconds >= max(1, seconds - 5)
        ):
            return {
                "frame": progress["frame"],
                "decoded_seconds": int(decoded_seconds),
                "wall_seconds": int(wall_seconds),
                "attempts": attempts,
            }
        if ready_source:
            break
        await asyncio.sleep(1)
    raise RuntimeError("Direct gateway RTSP diagnostic did not decode C201 media during the startup window")


async def run(mode: str, seconds: int) -> None:
    timeout = aiohttp.ClientTimeout(total=max(45, seconds + 40))
    async with aiohttp.ClientSession(timeout=timeout) as session:
        token = None
        if mode in {"setup", "setup-stream-only"}:
            deadline = time.monotonic() + 180
            while True:
                try:
                    token = await bootstrap(session)
                    break
                except aiohttp.ClientConnectorError:
                    if time.monotonic() >= deadline:
                        raise RuntimeError("Fresh Home Assistant did not start within 180 seconds")
                    await asyncio.sleep(2)
            if mode == "setup":
                await gateway_preflight(session)
            await setup_integration(session, token)
            save_cached_token(token)
        else:
            token = await cached_or_login(session)

        entity_id = await wait_c201_camera(session, token)
        if mode == "setup-stream-only":
            report_result({
                "phase": "setup-stream",
                "model_id": "xiaomi.camera.c201",
                "result": "ok",
            })
            return
        if mode == "setup":
            report_result({"phase": "setup", "model_id": "xiaomi.camera.c201", "result": "ok"})
            return
        if mode in {"snapshot", "verify"}:
            jpeg_bytes = await verify_snapshot(session, token, entity_id)
            report_result({"phase": "ha_camera_proxy", "model_id": "xiaomi.camera.c201", "jpeg_bytes": jpeg_bytes, "result": "ok"})
        if mode in {"stream", "verify"}:
            progress = await stream_seconds(session, token, entity_id, seconds)
            report_result({"phase": "ha_live_hls", "model_id": "xiaomi.camera.c201", **progress})
            if progress["result"] != "ok":
                raise RuntimeError("C201 Home Assistant HLS validation failed; see sanitized result above")
        if mode == "direct-rtsp":
            progress = await direct_rtsp_seconds(session, seconds)
            report_result({"phase": "direct_gateway_rtsp_diagnostic", "model_id": "xiaomi.camera.c201", "duration_seconds": seconds, "decoded_seconds": progress["decoded_seconds"], "wall_seconds": progress["wall_seconds"], "decoded_frames": progress["frame"], "result": "ok"})


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("mode", choices=("setup", "setup-stream-only", "snapshot", "stream", "direct-rtsp", "verify"))
    parser.add_argument("--seconds", type=int, default=60)
    arguments = parser.parse_args()
    asyncio.run(run(arguments.mode, arguments.seconds))

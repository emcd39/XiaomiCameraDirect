"""Isolated regressions for safe Home Assistant HLS soak diagnostics."""
from __future__ import annotations

import asyncio
import json
import subprocess
import sys
import unittest
from contextvars import ContextVar
from contextlib import redirect_stdout
from io import StringIO
from types import SimpleNamespace
from pathlib import Path
from unittest.mock import AsyncMock, patch

sys.path.insert(0, str(Path(__file__).parent))
import test_c201_ha
import ha_live_soak
import xlg_soak_live


class HlsDiagnosticsTests(unittest.IsolatedAsyncioTestCase):
    async def run_stream(self, stdout: bytes, returncode: int | None, wall: float = 20.0):
        with patch.object(test_c201_ha.shutil, "which", return_value="ffmpeg"), \
                patch.object(test_c201_ha, "camera_stream_url", new=AsyncMock(
                    return_value="http://127.0.0.1/api/hls/fake?token=fixture-secret")), \
                patch.object(test_c201_ha, "_stream_monotonic", side_effect=(0.0, wall)), \
                patch.object(test_c201_ha.asyncio, "to_thread", new=AsyncMock(
                    return_value=SimpleNamespace(returncode=returncode, stdout=stdout))):
            return await test_c201_ha.stream_seconds(None, "fixture-token", "camera.fixture", 20)

    async def test_short_hls_fails_decoded_threshold_but_keeps_wall_and_ffmpeg_metrics(self):
        result = await self.run_stream(b"frame=40\nout_time_ms=10000000\n", 0)
        self.assertEqual(result["result"], "failed")
        self.assertIn("decoded_duration_below_threshold", result["failures"])
        self.assertNotIn("wall_duration_below_threshold", result["failures"])
        self.assertEqual(result["entity_id"], "camera.fixture")
        self.assertEqual(result["frame"], 40)
        self.assertEqual(result["decoded_seconds"], 10)
        self.assertEqual(result["wall_seconds"], 20)
        self.assertEqual(result["ffmpeg_returncode"], 0)

    async def test_success_at_existing_duration_thresholds(self):
        result = await self.run_stream(b"frame=40\nout_time_ms=18000000\n", 0, wall=15.0)
        self.assertEqual(result["result"], "ok")
        self.assertEqual(result["failures"], [])

    async def test_ffmpeg_error_is_camera_scoped(self):
        result = await self.run_stream(b"frame=2\nout_time_ms=1000000\n", 7)
        self.assertEqual(result["entity_id"], "camera.fixture")
        self.assertEqual(result["ffmpeg_returncode"], 7)
        self.assertIn("ffmpeg_nonzero_exit", result["failures"])

    async def test_timeout_result_does_not_expose_signed_hls_address(self):
        timeout = subprocess.TimeoutExpired(
            ["ffmpeg", "http://127.0.0.1/api/hls/fake?token=fixture-secret"],
            80,
            output=b"frame=5\nout_time_ms=1000000\n",
        )
        with patch.object(test_c201_ha.shutil, "which", return_value="ffmpeg"), \
                patch.object(test_c201_ha, "camera_stream_url", new=AsyncMock(
                    return_value="http://127.0.0.1/api/hls/fake?token=fixture-secret")), \
                patch.object(test_c201_ha, "_stream_monotonic", side_effect=(0.0, 80.0)), \
                patch.object(test_c201_ha.asyncio, "to_thread", new=AsyncMock(side_effect=timeout)):
            result = await test_c201_ha.stream_seconds(None, "fixture-token", "camera.fixture", 20)
        self.assertEqual(result["entity_id"], "camera.fixture")
        self.assertIn("ffmpeg_timeout", result["failures"])
        self.assertNotIn("fixture-secret", repr(result))
        self.assertNotIn("/api/hls/", repr(result))

    async def test_two_camera_runner_reports_both_before_returning_failure_code(self):
        cameras = {"xiaomi.camera.c201": "camera.c201", "chuangmi.camera.026c02": "camera.mi360"}

        async def simulated_check(model, entity_id):
            is_early = model == "xiaomi.camera.c201"
            return {
                "model": model,
                "entity_id": entity_id,
                "frame": 40,
                "decoded_seconds": 10 if is_early else 20,
                "wall_seconds": 20,
                "ffmpeg_returncode": 0,
                "result": "failed" if is_early else "ok",
                "failures": ["decoded_duration_below_threshold"] if is_early else [],
            }

        output = StringIO()
        with redirect_stdout(output):
            reports = await ha_live_soak.run_camera_checks(cameras, simulated_check)
        lines = output.getvalue().splitlines()
        self.assertEqual(len(lines), 2)
        self.assertEqual({report["entity_id"] for report in map(json.loads, lines)}, set(cameras.values()))
        self.assertEqual(ha_live_soak.exit_code(reports), 1)
        failed = next(report for report in reports if report["result"] == "failed")
        self.assertEqual(failed["ffmpeg_returncode"], 0)
        self.assertEqual(failed["failures"], ["decoded_duration_below_threshold"])

    async def test_two_camera_runner_success_returns_zero_after_both_reports(self):
        cameras = {"xiaomi.camera.c201": "camera.c201", "chuangmi.camera.026c02": "camera.mi360"}

        async def simulated_check(model, entity_id):
            return {"model": model, "entity_id": entity_id, "result": "ok", "failures": []}

        output = StringIO()
        with redirect_stdout(output):
            reports = await ha_live_soak.run_camera_checks(cameras, simulated_check)
        self.assertEqual(len(output.getvalue().splitlines()), 2)
        self.assertEqual(ha_live_soak.exit_code(reports), 0)

    async def run_prepared_soak(self, failing_entity=None):
        class FakeSession:
            async def __aenter__(self):
                return self

            async def __aexit__(self, *_args):
                return False

        entry = "fixture-entry"
        entities = ["camera.c201", "camera.mi360"]
        current_entity = ContextVar("current_camera_entity", default=None)

        async def state_for(_session, _token, _method, path):
            entity_id = path.rsplit("/", 1)[-1]
            if entity_id == "camera.c201":
                attrs = {"model_id": "xiaomi.camera.c201", "source_codec": "H265", "source_resolution": "1920x1080"}
            else:
                attrs = {"model_id": "chuangmi.camera.026c02", "friendly_name": "Mi 360 Camera", "source_codec": "H265", "source_resolution": "1920x1080"}
            return {"entity_id": entity_id, "attributes": attrs}

        async def fake_stream_url(_session, _token, entity_id):
            current_entity.set(entity_id)
            return f"http://127.0.0.1/api/hls/{entity_id}?token=fixture-secret"

        def fake_clock():
            entity_id = current_entity.get()
            if entity_id is None:
                return 1000.0
            return 1020.0 if entity_id == failing_entity else 1600.0

        async def fake_to_thread(_call, command, **_kwargs):
            if isinstance(command, tuple) and len(command) == 1 and isinstance(command[0], list):
                command = command[0]
            if "-progress" in command:
                entity_id = command[command.index("-i") + 1].rsplit("/", 1)[-1].split("?", 1)[0]
                early = entity_id == failing_entity
                frames = 400 if early else 24000
                decoded_us = 10_000_000 if early else 600_000_000
                return SimpleNamespace(
                    returncode=0,
                    stdout=f"frame={frames}\nout_time_ms={decoded_us}\n".encode(),
                )
            return SimpleNamespace(
                returncode=0,
                stdout=b'{"streams":[{"codec_name":"h264","width":848,"height":480,"r_frame_rate":"20/1"}]}',
            )

        async def immediate_wait(condition, timeout):
            self.assertEqual(timeout, 38)
            await condition()

        patches = [
            patch.object(xlg_soak_live.aiohttp, "ClientSession", return_value=FakeSession()),
            patch.object(xlg_soak_live, "login", new=AsyncMock(return_value="fixture-token")),
            patch.object(Path, "read_text", return_value=entry),
            patch.object(xlg_soak_live, "websocket_result", new=AsyncMock(return_value=[
                {"entity_id": entity, "config_entry_id": entry} for entity in entities
            ])),
            patch.object(xlg_soak_live, "api", new=AsyncMock(side_effect=state_for)),
            patch.object(xlg_soak_live, "wait_for_condition", new=AsyncMock(side_effect=immediate_wait)),
            patch.object(test_c201_ha, "camera_stream_url", new=AsyncMock(side_effect=fake_stream_url)),
            patch.object(xlg_soak_live, "camera_stream_url", new=AsyncMock(side_effect=fake_stream_url)),
            patch.object(test_c201_ha.shutil, "which", return_value="ffmpeg"),
            patch.object(test_c201_ha, "_stream_monotonic", new=fake_clock),
            patch.object(xlg_soak_live.asyncio, "to_thread", new=AsyncMock(side_effect=fake_to_thread)),
        ]
        output = StringIO()
        with redirect_stdout(output):
            with patches[0], patches[1], patches[2], patches[3], patches[4], patches[5], patches[6], patches[7], patches[8], patches[9], patches[10]:
                try:
                    await xlg_soak_live.main()
                    status = 0
                except SystemExit as error:
                    status = error.code
        return status, output.getvalue()

    async def test_prepared_two_camera_script_reports_both_then_fails_nonzero(self):
        self.assertIs(xlg_soak_live.stream_seconds, test_c201_ha.stream_seconds)
        self.assertEqual(Path(xlg_soak_live.__file__).resolve(), Path(__file__).with_name("xlg_soak_live.py").resolve())
        self.assertEqual(Path(test_c201_ha.__file__).resolve(), Path(__file__).with_name("test_c201_ha.py").resolve())
        status, output = await self.run_prepared_soak(failing_entity="camera.c201")
        reports = [json.loads(line) for line in output.splitlines() if '"entity_id"' in line]
        self.assertEqual(status, 1)
        self.assertEqual({report["entity_id"] for report in reports}, {"camera.c201", "camera.mi360"})
        failed = next(report for report in reports if report["entity_id"] == "camera.c201")
        self.assertEqual(failed["ffmpeg_returncode"], 0)
        self.assertEqual(failed["frame"], 400, output)
        self.assertEqual(failed["decoded_seconds"], 10)
        self.assertEqual(failed["wall_seconds"], 20)
        self.assertEqual(failed["failures"], ["decoded_duration_below_threshold", "wall_duration_below_threshold"])
        self.assertNotIn("fixture-secret", output)
        self.assertNotIn("/api/hls/", output)

    async def test_prepared_two_camera_script_reports_both_and_succeeds(self):
        self.assertIs(xlg_soak_live.stream_seconds, test_c201_ha.stream_seconds)
        self.assertEqual(Path(xlg_soak_live.__file__).resolve(), Path(__file__).with_name("xlg_soak_live.py").resolve())
        self.assertEqual(Path(test_c201_ha.__file__).resolve(), Path(__file__).with_name("test_c201_ha.py").resolve())
        status, output = await self.run_prepared_soak()
        reports = [json.loads(line) for line in output.splitlines() if '"entity_id"' in line]
        self.assertEqual(status, 0, output)
        self.assertEqual({report["entity_id"] for report in reports}, {"camera.c201", "camera.mi360"})
        self.assertTrue(all(report["result"] == "ok" for report in reports))


if __name__ == "__main__":
    unittest.main(verbosity=2)

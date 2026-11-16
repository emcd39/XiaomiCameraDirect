import asyncio
import json
from pathlib import Path
import subprocess
import sys

import aiohttp

sys.path.insert(0, "/config/tests/e2e")
from test_c201_ha import camera_stream_url, login, stream_seconds
from test_ha import api, wait_for_condition, websocket_result
from ha_live_soak import exit_code, run_camera_checks


async def main():
    async with aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=60)) as session:
        token = await login(session)
        entry = Path("/config/.xlg-live-entry").read_text().strip()
        registry = await websocket_result(session, token, {"type": "config/entity_registry/list"})
        cameras = [row["entity_id"] for row in registry
                   if row.get("config_entry_id") == entry and row["entity_id"].startswith("camera.")]
        states = [await api(session, token, "GET", f"/api/states/{entity}") for entity in cameras]
        live = {state["attributes"].get("model_id"): state["entity_id"] for state in states
                if state["attributes"].get("model_id") == "xiaomi.camera.c201"
                or "mi 360" in state["attributes"].get("friendly_name", "").lower()}
        if len(live) != 2:
            raise RuntimeError(f"Expected two physical camera entities, matched {len(live)}")

        async def verify(model, entity):
            print(json.dumps({"model": model, "phase": "decode_start", "requested_seconds": 600}), flush=True)
            report = {"model": model, "entity_id": entity}
            try:
                frames = await stream_seconds(session, token, entity, 600)
                report.update(frames)
                if frames["result"] != "ok":
                    return report

                attrs = {}

                async def has_source_format():
                    state = await api(session, token, "GET", f"/api/states/{entity}")
                    attrs.update(state["attributes"])
                    return attrs.get("source_codec") == "H265" and bool(attrs.get("source_resolution"))

                await wait_for_condition(has_source_format, timeout=38)
                hls = await camera_stream_url(session, token, entity)
                probe = await asyncio.to_thread(subprocess.run, [
                    "ffprobe", "-v", "error", "-select_streams", "v:0",
                    "-show_entries", "stream=codec_name,width,height,r_frame_rate", "-of", "json", hls,
                ], capture_output=True, timeout=25)
                if probe.returncode:
                    report.update({"result": "failed", "failures": ["ffprobe_nonzero_exit"]})
                else:
                    stream = json.loads(probe.stdout)["streams"][0]
                    if stream["codec_name"] != "h264" or stream["width"] <= 0 or stream["height"] <= 0:
                        report.update({"result": "failed", "failures": ["unexpected_hls_format"]})
                    else:
                        report.update({"result": "ok", "source_codec": attrs["source_codec"],
                                       "source_resolution": attrs["source_resolution"], "ha_hls": stream})
            except Exception:
                # Avoid printing exception strings, which may contain signed HLS URLs.
                report.update({"result": "failed", "failures": ["source_or_probe_error"]})
            return report

        reports = await run_camera_checks(live, verify)
        if exit_code(reports):
            raise SystemExit(1)


if __name__ == "__main__":
    asyncio.run(main())

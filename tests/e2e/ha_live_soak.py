"""Run per-camera soak checks and print every report before aggregate failure."""
from __future__ import annotations

import asyncio
import json
from collections.abc import Awaitable, Callable, Mapping


CameraCheck = Callable[[str, str], Awaitable[Mapping[str, object]]]


async def run_camera_checks(cameras: Mapping[str, str], check: CameraCheck) -> list[dict[str, object]]:
    async def run_one(model: str, entity_id: str) -> dict[str, object]:
        try:
            report = dict(await check(model, entity_id))
        except Exception:
            # Avoid exception text because it may contain signed media URLs.
            report = {"result": "failed", "failures": ["camera_check_error"]}
        report.setdefault("model", model)
        report.setdefault("entity_id", entity_id)
        print(json.dumps(report, sort_keys=True), flush=True)
        return report

    return await asyncio.gather(*(run_one(model, entity) for model, entity in cameras.items()))


def has_failures(reports: list[Mapping[str, object]]) -> bool:
    return any(report.get("result") != "ok" for report in reports)


def exit_code(reports: list[Mapping[str, object]]) -> int:
    return 1 if has_failures(reports) else 0

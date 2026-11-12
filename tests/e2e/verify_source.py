"""Verify that Home Assistant is running the exact integration files in this checkout."""
from __future__ import annotations

import subprocess


subprocess.run(
    ["sha256sum", "-c", "/config/source.sha256"],
    cwd="/config",
    check=True,
)

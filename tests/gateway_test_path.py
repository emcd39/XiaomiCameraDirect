"""Resolve the gateway adapter in the checkout and in the isolated HA config."""

from pathlib import Path
import sys


def add_gateway_component_path():
    root = Path(__file__).resolve().parents[1]
    source_components = root / "legacy" / "custom_components"
    installed_components = root / "custom_components"
    components = source_components if source_components.is_dir() else installed_components
    sys.path.insert(0, str(components))

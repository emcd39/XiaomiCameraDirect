"""Discover direct Xiaomi cameras in a Home Assistant config and probe CS2 reachability.

Run from the repository root, for example:
    python -B scripts/ha_camera_reachability.py --ha-config-dir .test-env/ha-native-win-py314/config \
        --output .test-env/ha-native-win-py314/camera-reachability.json

The tool reads only the direct integration's camera inventory. It sends one
unicast CS2 LAN-search datagram to each private camera IP, never broadcasts,
and never changes routes, firewall rules, or Home Assistant configuration.
"""

import argparse
from datetime import datetime, timezone
from ipaddress import AddressValueError, IPv4Address, IPv4Network
import json
from pathlib import Path
import socket
import time

try:
    from scripts.ha_direct_preflight import (
        CS2_PORT, LAN_SEARCH, LOCAL_NETWORKS, PUNCH_REPLY, check_camera_network, route_source,
    )
except ModuleNotFoundError:  # Running as `python scripts/ha_camera_reachability.py`.
    from ha_direct_preflight import (
        CS2_PORT, LAN_SEARCH, LOCAL_NETWORKS, PUNCH_REPLY, check_camera_network, route_source,
    )


def _storage_path(config_dir: Path) -> Path:
    if config_dir.name == "core.config_entries":
        return config_dir
    return config_dir / ".storage" / "core.config_entries"


def load_camera_inventory(config_dir: Path) -> list[dict[str, object]]:
    """Load sanitized camera fields from HA storage without exposing account secrets."""
    path = _storage_path(config_dir)
    data = json.loads(path.read_text(encoding="utf-8-sig"))
    entries = data.get("data", {}).get("entries", [])
    cameras: list[dict[str, object]] = []
    seen: set[tuple[str, str]] = set()
    for entry in entries:
        if entry.get("domain") != "xiaomi_direct_pilot":
            continue
        for item in entry.get("data", {}).get("cameras", []):
            if not isinstance(item, dict):
                continue
            errors: list[str] = []
            raw_address = item.get("camera_ip")
            address = None
            if raw_address is None or not str(raw_address).strip():
                errors.append("missing_camera_ip")
            else:
                try:
                    address = IPv4Address(raw_address)
                except (AddressValueError, TypeError, ValueError):
                    errors.append("invalid_camera_ip")

            raw_network = item.get("camera_lan")
            network = None
            if raw_network is None or not str(raw_network).strip():
                errors.append("missing_camera_lan")
            else:
                try:
                    network = IPv4Network(raw_network, strict=False)
                except (AddressValueError, TypeError, ValueError):
                    errors.append("invalid_camera_lan")
            if address is not None and network is not None and not check_camera_network(address, network):
                errors.append("invalid_camera_subnet")

            device_id = str(item.get("device_id", ""))
            key = (device_id, str(address or raw_address or ""))
            if key != ("", "") and key in seen:
                continue
            if key != ("", ""):
                seen.add(key)

            configured_source = item.get("source_ip")
            configured_source_error = None
            if configured_source:
                try:
                    configured_source = IPv4Address(configured_source)
                except (AddressValueError, TypeError, ValueError):
                    configured_source_error = "invalid_configured_source_ip"
                    configured_source = None
            cameras.append({
                "name": str(item.get("name") or item.get("model") or address or raw_address or "Unknown camera"),
                "model": str(item.get("model") or "unknown"),
                "camera_ip": address,
                "camera_lan": network,
                "camera_ip_raw": str(raw_address) if raw_address is not None else None,
                "camera_lan_raw": str(raw_network) if raw_network is not None else None,
                "configured_source_ip": configured_source,
                "configured_source_error": configured_source_error,
                "inventory_errors": errors,
            })
    return cameras


def probe_cs2_unicast(camera: IPv4Address, source: IPv4Address, timeout: float) -> bool:
    """Send one targeted CS2 discovery packet and accept only a valid camera reply."""
    deadline = time.monotonic() + timeout
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as probe:
        probe.bind((str(source), 0))
        probe.sendto(LAN_SEARCH, (str(camera), CS2_PORT))
        while (remaining := deadline - time.monotonic()) > 0:
            probe.settimeout(remaining)
            try:
                data, peer = probe.recvfrom(2048)
            except TimeoutError:
                break
            if (
                peer[0] == str(camera)
                and len(data) >= 4
                and data.startswith(PUNCH_REPLY)
                and int.from_bytes(data[2:4], "big") == len(data) - 4
            ):
                return True
    return False


def inspect_camera(camera: dict[str, object], timeout: float) -> dict[str, object]:
    address = camera["camera_ip"]
    network = camera["camera_lan"]
    configured_source = camera["configured_source_ip"]
    inventory_errors = camera.get("inventory_errors", [])
    result = {
        "name": camera["name"],
        "model": camera["model"],
        "camera_ip": str(address) if address is not None else camera.get("camera_ip_raw"),
        "camera_lan": str(network) if network is not None else camera.get("camera_lan_raw"),
        "configured_source_ip": str(configured_source) if configured_source else None,
        "selected_source_ip": None,
        "source_in_camera_lan": False,
        "configured_source_matches_route": None,
        "cs2_result": "not_checked",
        "direct_usable": False,
        "status": "route_unavailable",
    }
    if camera.get("configured_source_error"):
        result["configured_source_error"] = camera["configured_source_error"]
    if inventory_errors:
        result["inventory_errors"] = inventory_errors

    if address is None:
        result["status"] = next(
            (error for error in inventory_errors if error in {"missing_camera_ip", "invalid_camera_ip"}),
            "invalid_camera_ip",
        )
        return result
    if not any(address in private_network for private_network in LOCAL_NETWORKS):
        result["status"] = "non_private_camera_ip"
        return result

    try:
        source = route_source(address)
    except OSError as error:
        result["status"] = "route_unavailable"
        result["route_error"] = type(error).__name__
        return result

    valid_camera_lan = network is not None and check_camera_network(address, network)
    source_in_lan = valid_camera_lan and source in network
    result["selected_source_ip"] = str(source)
    result["source_in_camera_lan"] = source_in_lan
    result["configured_source_matches_route"] = configured_source == source
    try:
        replied = probe_cs2_unicast(address, source, timeout)
    except OSError as error:
        result["cs2_result"] = "socket_error"
        result["probe_error"] = type(error).__name__
        result["status"] = "probe_socket_error"
        return result

    result["cs2_result"] = "reply" if replied else "timeout"
    direct_usable = (
        valid_camera_lan and not inventory_errors and source_in_lan and replied
        and result["configured_source_matches_route"] and not camera.get("configured_source_error")
    )
    result["direct_usable"] = direct_usable
    if direct_usable:
        result["status"] = "available"
    elif replied and "missing_camera_lan" in inventory_errors:
        result["status"] = "cs2_reply_missing_camera_lan"
    elif replied and "invalid_camera_lan" in inventory_errors:
        result["status"] = "cs2_reply_invalid_camera_lan"
    elif replied and "invalid_camera_subnet" in inventory_errors:
        result["status"] = "cs2_reply_invalid_camera_subnet"
    elif replied and not source_in_lan:
        result["status"] = "routed_reply_but_integration_requires_same_subnet"
    elif replied and not result["configured_source_matches_route"]:
        result["status"] = "reachable_after_source_refresh"
    elif "missing_camera_lan" in inventory_errors:
        result["status"] = "missing_camera_lan_cs2_timeout"
    elif "invalid_camera_lan" in inventory_errors:
        result["status"] = "invalid_camera_lan_cs2_timeout"
    elif "invalid_camera_subnet" in inventory_errors:
        result["status"] = "invalid_camera_subnet_cs2_timeout"
    elif not source_in_lan:
        result["status"] = "source_outside_camera_subnet"
    else:
        result["status"] = "cs2_timeout"
    return result


def build_report(config_dir: Path, timeout: float, *, model: str | None = None) -> dict[str, object]:
    cameras = load_camera_inventory(config_dir)
    if model is not None:
        cameras = [camera for camera in cameras if camera["model"] == model]
    results = [inspect_camera(camera, timeout) for camera in cameras]
    return {
        "checked_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        "ha_config_dir": str(config_dir),
        "model_filter": model,
        "probe": {"protocol": "CS2/UDP", "port": CS2_PORT, "packets_per_probed_camera": 1,
                  "broadcast": False, "timeout_seconds": timeout,
                  "skips_missing_invalid_or_public_camera_ip": True},
        "summary": {
            "camera_count": len(results),
            "direct_usable_count": sum(bool(camera["direct_usable"]) for camera in results),
            "reachable_after_source_refresh_count": sum(
                camera["status"] == "reachable_after_source_refresh" for camera in results
            ),
        },
        "cameras": results,
    }


def main() -> int:
    repo = Path(__file__).resolve().parents[1]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--ha-config-dir", type=Path,
        default=repo / ".test-env" / "ha-native-win-py314" / "config",
        help="Home Assistant config directory (contains .storage/core.config_entries)",
    )
    parser.add_argument("--timeout", type=float, default=2.0, help="CS2 reply timeout per camera")
    parser.add_argument("--model", help="Probe only cameras with this exact Xiaomi model ID")
    parser.add_argument("--output", type=Path, help="Optional JSON report path")
    args = parser.parse_args()
    if not 0 < args.timeout <= 15:
        parser.error("--timeout must be greater than 0 and at most 15 seconds")
    try:
        report = build_report(args.ha_config_dir, args.timeout, model=args.model)
    except (OSError, ValueError, KeyError, json.JSONDecodeError) as error:
        parser.exit(2, f"Inventory error: {type(error).__name__}: {error}\n")
    if not report["cameras"]:
        if args.model is not None:
            parser.exit(2, f"No direct cameras match Xiaomi model {args.model!r}.\n")
        parser.exit(2, "No xiaomi_direct_pilot cameras found in Home Assistant storage.\n")
    output = json.dumps(report, ensure_ascii=False, indent=2)
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(output + "\n", encoding="utf-8")
    print(output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

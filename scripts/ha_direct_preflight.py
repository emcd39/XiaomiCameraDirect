"""Run from the target HA network namespace before the direct camera pilot."""

import argparse
from ipaddress import IPv4Address, IPv4Network
import json
import socket
import time


CS2_PORT = 32108
LAN_SEARCH = b"\xf1\x30\x00\x00"
PUNCH_REPLY = b"\xf1\x41"
LOCAL_NETWORKS = tuple(
    IPv4Network(cidr) for cidr in ("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16")
)


def check_camera_network(camera, network):
    return (
        any(network.subnet_of(private) for private in LOCAL_NETWORKS)
        and network.prefixlen <= 30
        and camera in network
        and camera not in (network.network_address, network.broadcast_address)
    )


def route_source(camera):
    # Connecting a UDP socket without sending asks the kernel to select a source.
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as route:
        route.connect((str(camera), CS2_PORT))
        return IPv4Address(route.getsockname()[0])


def find_camera(camera, network, source, timeout):
    start = time.monotonic()
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as probe:
        probe.bind((str(source), 0))
        probe.setsockopt(socket.SOL_SOCKET, socket.SO_BROADCAST, 1)
        for address in (camera, network.broadcast_address):
            probe.sendto(LAN_SEARCH, (str(address), CS2_PORT))

        while (remaining := timeout - (time.monotonic() - start)) > 0:
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


def preflight(camera, network, *, probe=False, timeout=5):
    if not check_camera_network(camera, network):
        return "invalid_camera_subnet", 2

    try:
        source = route_source(camera)
    except OSError:
        return "route_unavailable", 2
    if source not in network:
        return "source_outside_camera_subnet", 2
    if source == camera:
        return "camera_is_local_host", 2
    if not probe:
        return "route_candidate", 0

    try:
        answered = find_camera(camera, network, source, timeout)
    except OSError:
        return "discovery_socket_error", 2
    if not answered:
        return "cs2_lan_search_timeout", 1
    return "cs2_lan_reply", 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("camera", type=IPv4Address, help="C201 IPv4 address")
    parser.add_argument("subnet", type=IPv4Network, help="Camera LAN CIDR")
    parser.add_argument("--probe", action="store_true", help="Send unicast and directed broadcast LAN search")
    parser.add_argument("--timeout", type=int, default=5, choices=range(1, 16), metavar="1..15")
    args = parser.parse_args()

    start = time.monotonic()
    result, code = preflight(args.camera, args.subnet, probe=args.probe, timeout=args.timeout)
    print(json.dumps({
        "mode": "discovery" if args.probe else "route_only",
        "result": result,
        "elapsed_ms": round((time.monotonic() - start) * 1000),
    }))
    return code


if __name__ == "__main__":
    raise SystemExit(main())

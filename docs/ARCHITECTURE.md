# Architecture / ADR 001

## Objectives

- Original Xiaomi firmware, no video relay through Xiaomi cloud
- Single owner of each Xiaomi media session
- Home Assistant and Frigate as independent consumers
- PTZ, audio, live view, presets where supported by model
- Graceful recovery with observable errors, never false online indicators
- Keep protocol backends independent from the public gateway API

## Current MVP (working code, conditional on the upstream Xiaomi backend)

```text
                         Home Assistant Green
                         HACS camera + PTZ buttons
                                   |
                            Gateway HTTP API
                                   |
                       ONVIF adapter (RelativeMove)
                                   |
                upstream xiaomi-onvif service [optional]
                                   |
                      patched go2rtc Xiaomi producer
                       /                      \
                 Xiaomi LAN camera          RTSP:8554
                                                 |
                                              Frigate
```

The gateway is **not** yet a Xiaomi protocol implementation. It wraps an ONVIF
endpoint exposed by `xiaomi-onvif`, an explicitly separate beta dependency.
Stream URLs are passed to Home Assistant and Frigate rather than decoded inside
Home Assistant. A stream that is not actually running is not shown as online by
our API. GET /readyz means the *configuration loaded*, not the camera is online.

### Why not use the Windows viewer directly?

The original `xiaomi-camera-viewer` is an MIT-licensed C++ Windows app with a Go
bridge. Re-use protocol research and tests, not Direct3D UI code. A future native
Go adapter may port applicable MIT-licensed bridge logic with proper notices.

### Native backend (planned)

Interface: `CameraBackend` provides `Discover`, `Open`, `Media`, `Step`, `Preset`,
`Status`, `Close`. A `SessionManager` owns stream reconnects and prevents
multiple PTZ consumers from racing. Separate model drivers own quality and
transport quirks (C200/C300/CW300/CW400/CW500, etc).

- Auth: cloud **only** for device inventory and MISS session key negotiation
- Media: CS2/MISS via UDP/TCP in LAN; refuse WAN/relay fallback
- Stream: native H.264/H.265 packets to go2rtc/RTSP, without transcoding by default
- Control: PTZ `0x112` / `{"operation":1..4}` verified for CW400/CW500 only
- Errors: show `AUTH_REQUIRED`, `KEY_EXPIRED`, `DISCOVERY_FAILED`, `LAN_UNREACHABLE`
- Offline: no promise of new sessions when Xiaomi cloud is unreachable

## Security

Current gateway HTTP API requires Bearer token on all management endpoints.
REST listener defaults to loopback. For LAN operation configure a non-loopback
IP AND long token, restrict reachability by firewall, preferably use HTTPS
reverse proxy. `/healthz` and `/readyz` carry no credentials.

**Important**: An unprotected upstream ONVIF server can move a physical camera.
Never expose ports 1984, 8554, 8891, 8099 to the internet. The `xiaomi-onvif`
upstream currently does not authenticate ONVIF; isolate by firewall/network.

No Xiaomi account passwords, tokens or private keys should be committed.
The gateway API token is passed as an environment secret. The backend Xiaomi
account credentials are configured out-of-band using its official login flow.

## API version v1

- `GET /healthz` (no auth): gateway process liveness
- `GET /readyz` (no auth): parsed configuration count only
- `GET /v1/cameras`: registered camera metadata and RTSP stream URL
- `POST /v1/cameras/{id}/ptz/step` with `{ "direction": "left" }`
- `POST /v1/cameras/{id}/ptz/stop` with empty JSON body
- `POST /v1/cameras/{id}/ptz/preset` with `{ "token": "1" }`
- `GET /v1/frigate/config`: YAML snippet for existing Frigate config

All `/v1/` require `Authorization: Bearer <XLG_API_TOKEN>`.
Video and ONVIF are managed by the configured backend, not proxied via gateway API.

## Non-goals for MVP

No claimed zero-cloud key generation, no automatic Xiaomi account sign-in, no
verified Xiaomi model support, no automatic addon install, no Frigate config
mutation, no click-and-drag PTZ card, and no fake motion sensor. Those are
explicit future milestones after testing on real cameras.

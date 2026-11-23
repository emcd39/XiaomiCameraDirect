# Xiaomi Camera Direct

Xiaomi cameras in Home Assistant with video and supported controls over the
local network. Home Assistant uses the Xiaomi account for sign-in, inventory,
and the keys needed to open a new local camera session.

## Install in Home Assistant

Requires Home Assistant Core 2026.10.4 or later and network access from Home
Assistant to the cameras.

1. Open **HACS → ⋮ → Custom repositories**, add
   `https://github.com/moukas/XiaomiCameraDirect` as **Integration**, then
   download **Xiaomi Camera Direct** and restart Home Assistant.
2. Open **Settings → Devices & services → Add integration** and choose
   **Xiaomi Camera Direct**. Select your Xiaomi Home region and approve
   access to the account.
3. Use **Configure** on the account entry to select cameras. All discovered
   cameras are included by default. Use the camera entity for live video,
   snapshots and short HA recordings. PTZ buttons appear for the models with
   a mapped directional profile and become available during a live session.
   For a C400 in the European region, select the camera under **C400 cameras
   to stream at lower resolution** to reduce bandwidth. Saving reloads the
   account entry and applies the selected profile when the stream next opens.
   The integration's experimental microphone options are off by default. See
   the [model capability matrix](docs/HA-CAMERA-CAPABILITIES.md) for audio and
   other features verified on each model.

For a manual installation, copy the complete
`custom_components/xiaomi_direct_pilot` directory into the Home Assistant
configuration directory under `custom_components`, restart and start at step 2.
Existing direct installations use the same integration domain and config entries.
The optional dashboard card is bundled with the integration. The example
[WebRTC dashboard](docs/examples/lovelace-xiaomi-direct.json) also needs
[WebRTC Camera](https://github.com/AlexxIT/WebRTC) and entity IDs from your HA.

The direct integration runs inside HA Core. The Go gateway is available
separately for diagnostics, reference testing and standalone deployments.
See the [direct integration test record](docs/HA-DIRECT-PILOT.md) for hardware
results and the [delivery plan](docs/HA-DIRECT-PLAN.md) for Frigate and
remaining acceptance tests.

## Xiaomi Local Gateway

The bridge can use an existing RTSP/ONVIF server such as the `xiaomi-onvif`
patched-go2rtc pair. Managed mode uses an authorized Xiaomi account to discover
cameras and negotiate local media for supported profiles. See the current
[camera evidence](docs/LOCAL-CAMERA-PROGRESS.md) and [managed API](docs/API-MANAGED.md).
The [direct Home Assistant integration plan](docs/HA-DIRECT-PLAN.md) covers
running without the gateway, the optional Frigate export, and how both
backends will be tested.

## Architecture

```text
Xiaomi cameras ── LAN MISS/CS2 ── patched go2rtc + xiaomi-onvif (external)
                                      │ RTSP    │ ONVIF
                                      │         └── Gateway Go REST API
                                      │                  └── HA PTZ buttons
                                      ├── Home Assistant (camera stream)
                                      └── Frigate (record/detect/ONVIF PTZ)
```

The diagram shows bridge mode. In managed mode the Go gateway runs the native
MISS/CS2 session and supplies its own RTSP source.

### Managed pilot on native Linux

Use a Linux host with a network interface on the cameras' LAN so the gateway
can receive CS2 discovery replies. The following example assumes Home Assistant
can reach the shown Linux host address. Substitute the host's actual LAN IP.

```sh
docker build --tag xlg-gateway:local .
docker run -d --name xlg-gateway --restart unless-stopped --network host \
  --mount type=volume,source=xlg-managed-data,target=/data \
  --env-file /path/to/private/xlg-managed.env \
  xlg-gateway:local -managed -listen 192.168.1.20:8099
```

In the private environment file, set `XLG_API_TOKEN` to a long random token
and provide the authorized account as `XLG_XIAOMI_USER_ID`,
`XLG_XIAOMI_REGION`, and `XLG_XIAOMI_TOKEN`. Set `XLG_RTSP_ADVERTISE_HOST`
to the Linux host's LAN IP, and `XLG_RTSP_LISTEN` to that IP on port 8554
(for example, `192.168.1.20:8554`). Add the Gateway integration in HA with the
HTTP address and API token. The API and RTSP are reachable on the specified
Linux interface. Keep them on your trusted network.

## Run the standalone gateway

1. Prepare an RTSP + ONVIF source, ideally using the upstream instructions in
   [examples/UPSTREAM.md](examples/UPSTREAM.md). **Verify that its RTSP works
   from your Frigate/HA machine and ONVIF PTZ is available.**
2. `cp config.example.json config.json` and set the real LAN endpoints.
   If running in Docker on Synology, change `listen` to a LAN interface address.
3. `cp .env.example .env`, then generate a fresh long token using
   `openssl rand -hex 32`, put it in `.env` as `XLG_API_TOKEN`.
4. `docker compose -f compose.example.yaml up -d --build`, or run Go directly:

```bash
XLG_API_TOKEN='your-long-random-token' go run ./cmd/gateway -config config.json
```

5. Test (substitute the real gateway address and token):

```bash
curl -H "Authorization: Bearer YOUR_TOKEN" http://127.0.0.1:8099/v1/cameras
curl -H "Authorization: Bearer YOUR_TOKEN" http://127.0.0.1:8099/v1/frigate/config
curl -X POST -H "Authorization: Bearer YOUR_TOKEN" -H "Content-Type: application/json" \
  -d '{"direction":"left"}' http://127.0.0.1:8099/v1/cameras/garden/ptz/step
```

6. Copy `legacy/custom_components/xiaomi_local_gateway` into the
   HA `config/custom_components` folder. Restart HA and add the **Xiaomi Local
   Gateway** integration from Settings → Devices & Services. Enter the gateway
   LAN URL and token. The integration provides camera entities (RTSP) and PTZ
   directional buttons. Dashboard live view depends on codec compatibility.
7. Merge the generated Frigate snippet with your current config. Leave the
   Frigate go2rtc restream and RTSP recording roles as generated, but configure
   object detection resolution and MQTT according to your installation.

## Reliability and security

- Configuration checked strictly before starting; duplicate camera IDs rejected.
- HTTP endpoints use bounded timeouts and no unsafe automatic PTZ retries.
- REST API requires Bearer token and defaults to binding on 127.0.0.1.
- No public camera video exposure by default; upstream RTSP/ONVIF needs its own firewall.
- No false camera health claim: `readyz` means only that the gateway config loaded.
- No camera credentials hardcoded in this repo.
- **Xiaomi cloud is still required to negotiate new MISS sessions with stock firmware.**
- Gateway listens on HTTP, so avoid untrusted LAN and expose via TLS reverse
  proxy when crossing network trust boundaries.
- Upstream xiaomi-onvif is in beta; pin versions, test before update.

## Development

On Windows, run `./scripts/test-camera-quick.ps1` to compile and test inside a
disposable WSLc Linux container. Build the deployment image with
`wslc build --tag xlg-gateway:local .`. On Linux, use the same Dockerfile or run
`sh scripts/test-camera-quick.sh` with Go and Node installed. The
[integration test guide](tests/README.md) describes the isolated HA stack.

```bash
go test -race ./...
go vet ./...
go run ./cmd/gateway -check-config -config config.example.json
python -m compileall -q custom_components legacy/custom_components
```

See the [roadmap](docs/ROADMAP.md), [architecture](docs/ARCHITECTURE.md), and
[HACS publishing notes](docs/PUBLISH.md).

## External references

- [xiaomi-camera-viewer](https://github.com/lubos-hnanicek/xiaomi-camera-viewer) (MIT) — MISS/CS2 key negotiation, LAN discovery, PTZ CW400/CW500
- [xiaomi-onvif](https://github.com/SincereXIA/xiaomi-onvif) (MIT) — patched go2rtc, ONVIF PTZ/Frigate
- [ha-xiaomi-camera](https://github.com/willjayyyy/ha-xiaomi-camera) (Apache-2.0) — HA/add-on UX inspiration
- [Frigate restream documentation](https://docs.frigate.video/configuration/restream/)

External components are not bundled; their licences apply separately. The
native gateway is MIT-licensed. See `LICENSE`.

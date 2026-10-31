# Xiaomi Local Gateway

**Status: v0.1.0 — M0 developer preview, NOT a production-ready Xiaomi integration.**

One integration layer for Xiaomi cameras, Home Assistant and Frigate. Built in
Go with a small Python HACS integration. Inspired by independent Xiaomi reverse
engineering without copying the Windows UI.

**M0 works as a bridge over an *existing* RTSP+ONVIF server**, such as the
`xiaomi-onvif` patched-go2rtc pair. It does **not yet implement Xiaomi MISS
transport**, Xiaomi account login, stream session management, autoinstall or
zero-cloud operation. Do not deploy as a security-critical recorder yet.

## Architecture

```text
Xiaomi cameras ── LAN MISS/CS2 ── patched go2rtc + xiaomi-onvif (external)
                                      │ RTSP    │ ONVIF
                                      │         └── Gateway Go REST API
                                      │                  └── HACS PTZ buttons
                                      ├── Home Assistant (camera stream)
                                      └── Frigate (record/detect/ONVIF PTZ)
```

We will replace the external Xiaomi-specific bridge with our own tested
protocol backend in M2. The Go REST API, camera entity, Frigate mappings and
configuration remain stable when doing so.

## Run the developer preview

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

6. In HACS, add this directory as a custom integration repo **once you publish
   the repository**, or copy `custom_components/xiaomi_local_gateway` into the
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

## Repository publication

This ZIP contains an initialized **local Git repository**, not a published GitHub
repository. The module/import path and manifest links currently use the
placeholder owner `xiaomi-local-gateway` and **must be changed to your real
GitHub owner/repository** before publishing. No GitHub repository is created
by this package. See [docs/PUBLISH.md](docs/PUBLISH.md).

## Development

```bash
go test -race ./...
go vet ./...
go run ./cmd/gateway -check-config -config config.example.json
python -m compileall -q custom_components
```

Roadmap: [docs/ROADMAP.md](docs/ROADMAP.md), architecture: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## External references

- [xiaomi-camera-viewer](https://github.com/lubos-hnanicek/xiaomi-camera-viewer) (MIT) — MISS/CS2 key negotiation, LAN discovery, PTZ CW400/CW500
- [xiaomi-onvif](https://github.com/SincereXIA/xiaomi-onvif) (MIT) — patched go2rtc, ONVIF PTZ/Frigate
- [ha-xiaomi-camera](https://github.com/willjayyyy/ha-xiaomi-camera) (Apache-2.0) — HA/add-on UX inspiration
- [Frigate restream documentation](https://docs.frigate.video/configuration/restream/)

External components are not bundled; their licences apply separately. The
native gateway is MIT-licensed. See `LICENSE`.

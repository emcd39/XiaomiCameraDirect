# Development roadmap

## Active delivery plan — simple setup and reliable operation

The [C201 delivery plan](DELIVERY-PLAN.md) defines the user-facing scope,
physical acceptance tests, and release criteria for video, camera movement,
and the other camera features.

The implementation order is now defined in [SIMPLE-STABLE-PLAN.md](SIMPLE-STABLE-PLAN.md)
(Czech), with a bounded handoff prompt in [NEXT-TASK.md](NEXT-TASK.md).
Prioritize a verified C201 backend, one managed runtime, HA onboarding,
persistent settings and recovery. Native MISS development is deferred until
hardware evidence shows it is necessary. The milestones below remain the
original scope inventory; they are not the current implementation order.

## Milestone M0 — architecture + testable skeleton (in this ZIP)
- [x] Go API with auth, PTZ adapter, structured camera inventory
- [x] HACS config flow and HA camera/button entities
- [x] Frigate RTSP/ONVIF config generator
- [x] Dockerfile, Compose example, Go test suite, CI
- [x] Reproduce the local HA Container mock integration; real video remains unverified
- [ ] Validate ONVIF commands against a physical Xiaomi camera

## M1 — end-to-end pilot
- [ ] Test real Xiaomi camera model + firmware + region
- [ ] Connect upstream `xiaomi-onvif` and patched `go2rtc` to same device
- [ ] Verify H.265 RTSP, low-latency playback and PTZ mapping
- [ ] Verify Frigate restream, recordings, relative PTZ & tracking
- [ ] 72-hour session soak test and reconnect after router reboot
- [ ] Positive/negative test of cloud outage during stream vs reconnect

## M2 — native `xiaomi-miss` backend, eliminating beta bridge dependency
- [ ] Protocol adapter interface + reusable session manager
- [ ] Implement signed key retrieval, broadcast discovery, LAN-only CS2
- [ ] Import/rewrite tested MISS media parser + audio codec support
- [ ] Native PTZ including per-model quirks and PTZ stop semantics
- [ ] Multi-camera; one active transport session per camera
- [ ] Combined dual-lens CW500 feed where verified
- [ ] Quality selection and stream/recording diagnostics

## M3 — one-click appliance for HA / Frigate
- [ ] HACS package release; HA OS Supervisor add-on repo
- [ ] Wizard for Xiaomi account auth, camera discovery and device selection
- [ ] Automatic bridge startup and config migration
- [ ] Live stream status sensors, repair notices, preview snapshots
- [ ] Presets, lens power / IR / privacy controls where supported
- [ ] Motion events via Frigate MQTT only (avoid duplicate ML pipeline)
- [ ] Versioned Docker images with SBOM and upgrade rollback

## Compatibility matrix

| Model ID | Video | PTZ | Preset | FW | Status |
| --- | --- | --- | --- | --- | --- |
| `isa.camera.hlc8a` (CW400) | reference project evidence | reference project evidence | unknown | unknown | not tested here |
| `isa.camera.500dh` (CW500) | reference project evidence | reference project evidence | unknown | unknown | not tested here |
| `chuangmi.camera.026c02` | candidate | requires verification | unknown | unknown | not tested here |

No green compatibility badge until real end-to-end CI/test captures succeed.

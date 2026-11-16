# Camera compatibility

Test labels distinguish the product name, owner-reported identifier, protocol model code, and real video/PTZ results. One does not imply another.

| Camera | Owner-reported identifier | Firmware / plugin | Account region | Video | PTZ |
| --- | --- | --- | --- | --- | --- |
| Xiaomi Smart Camera C201 | `1213911322` (shown under “ID”; field meaning not confirmed); linked account inventory reports `xiaomi.camera.c201` | `5.3.2_0016` / `4560.8.1-8` | EU | Prior direct/local decode evidence; current gateway first-media behavior has separate open results | Four physical directions documented; not re-run from current demo package |
| Xiaomi Smart Camera C400 | Account inventory: `chuangmi.camera.039a04` | Firmware not present in inventory | `de` | Experimental profile, 2560×1440 live in test HA. CS2 transport is negotiated. | Experimental mapping; no physical PTZ write through the gateway |
| Mi 360 | Account inventory: `chuangmi.camera.026c02` | Firmware not present in inventory | `de` | Experimental gateway profile; live H.265 preview over CS2/UDP, with earlier UDP timeouts | One left step through the gateway showed scene movement consistent with a left pan; other directions and HA acceptance pending |
| Xiaomi Outdoor Camera AW300 | Account inventory: `mxiang.camera.mod13` | Firmware not present in inventory | `de` | Experimental video profile. One live HA decode passed over CS2/UDP; later starts timed out before the first frame. | No verified mapping |

The current candidate results and remaining acceptance steps are recorded in
[LOCAL-CAMERA-PROGRESS.md](LOCAL-CAMERA-PROGRESS.md).

The AW300 identification comes from the catalog entry for
[`mxiang.camera.mod13`](https://home.miot-spec.com/spec/mxiang.camera.mod13).
Xiaomi lists H.265, 2304×1296 and microSD support for the
[AW300 product](https://www.mi.com/global/product/xiaomi-outdoor-camera-aw300/specs/).
The firmware is still needed to qualify AW300 support. An
[upstream field report](https://github.com/AlexxIT/go2rtc/issues/2291) also
reports a TCP session without media on a different AW300.
After video and reconnect are verified, record separate acceptance results for
quality selection, playback from SD, night vision, the spotlight, detection,
and each direction of two-way audio. Xiaomi documents these in its
[AW300 monitoring guide](https://www.mi.com/global/support/faq/details/KA-522328/)
and [function settings guide](https://www.mi.com/global/support/faq/details/KA-522216/).

Unknown Xiaomi models can be added through a local experimental profile file,
but the camera's model/firmware, session transport and motor mapping must first
be established. The file mechanism does not infer PTZ from inventory or enable
an unknown motor protocol. See [PTZ profiles](PTZ-PROFILES.md).

## C201 investigation, 2026-11-05

- The owner supplied local address `192.168.3.119`. A read-only probe received an ICMP echo reply. TCP connection attempts to ports 80, 443, 554, 8554, 8891, and 1984 did not connect. These common ports do not identify the camera's protocol; no credentials, stream, or control requests were sent.
- Xiaomi Home labels `1213911322` as “ID”. That value alone does not confirm a protocol model code. The public MIoT catalog lists `xiaomi.camera.c201`, but that community metadata is not a handshake or account inventory result.
- T00 verified the integration and gateway against local mocks only. It did not emulate C201, Xiaomi login, or real video/PTZ.

## C201 physical pilot recheck, 2026-11-06

- The Xiaomi EU account remains linked in the temporary bridge and the inventory reports model `xiaomi.camera.c201`. This confirms the earlier model-code hypothesis for this account.
- The official stream path did not establish a video session and previously timed out after 30 seconds. On the compatibility path, the control API returned a credential-free local RTSP source and no stream-configuration error. PyAV could not open a valid video stream and returned `InvalidDataError` on both TCP and UDP probes in about five seconds. A snapshot request returned HTTP 503 with `StillsError`. No JPEG or decoded frame was produced.
- The local go2rtc stream listing contains a `xiaomi://` source with a model field matching the account inventory. RTSP `OPTIONS` returns 200, but `DESCRIBE` returns 404 after about five seconds with no SDP. PyAV reports `InvalidDataError` over both TCP and UDP. The filtered go2rtc log contained no C201-specific error. This locates the stop between source configuration and a usable RTSP description. The reason the Xiaomi source is not activated remains unconfirmed. The [pinned go2rtc C201 report](https://github.com/AlexxIT/go2rtc/issues/2290) is a lead, not a physical compatibility result.
- Headless recheck using the already configured local bridge authorization confirmed `linked=true`, one C201 record, `online=true`, `lan_online=false`, path `compat`, `publishable=true`, no stream-configuration error, no measured FPS, and no active official SDK session for C201. One fresh snapshot request with `max_age=0` returned HTTP 503; sanitized classification was `RTSP_SOURCE_FAILURE`. No JPEG was received or saved. No raw response body, device ID, or credentials were written to evidence.
- The bridge log command returned zero lines. The available Ubuntu-24.04 WSL2 interface is on a separate virtual subnet and routes through its virtual gateway; no Linux host on the camera's `192.168.3.0/24` LAN was available. A same-LAN comparison against a known-working reference stream remains pending.
- The pilot container shares the test HA network namespace. No production Home Assistant, router, or camera settings were changed. Direct LAN delivery, relay use, ten-minute playback, recovery, and physical PTZ remain unverified.

## Backend candidates reviewed for T01

The source snapshots below were inspected on 2026-11-05. Findings are pinned to those commits, not to moving branches or floating container tags. Neither candidate has been tested with this camera.

| Candidate and exact source revision | License and platforms | Account setup | Local video and C201 evidence |
| --- | --- | --- | --- |
| [`SincereXIA/xiaomi-onvif`](https://github.com/SincereXIA/xiaomi-onvif/commit/d0e26d1633a98621c7e8650e56ca81f72bfa7e69), commit `d0e26d1633a98621c7e8650e56ca81f72bfa7e69` (2026-10-30; add-on metadata 0.1.3). Bundles patched go2rtc and an ONVIF bridge; its recorded go2rtc base is [`a71f29545c8ceefa4224b201d1d34ff937300fa8`](https://github.com/AlexxIT/go2rtc/commit/a71f29545c8ceefa4224b201d1d34ff937300fa8). | Project and recorded go2rtc base are MIT. Docker image is multi-architecture; experimental HA add-on declares amd64/aarch64, with real HAOS validation still pending. | Not a managed Xiaomi account flow in HA. The example requires manually obtaining the go2rtc Xiaomi account ID/token and setting Xiaomi stream configuration. Keep both as local secrets. | Needs go2rtc-to-camera local network access and Internet access for encryption-key refresh. The reviewed docs do not establish a LAN-only guarantee. Published hardware table verifies only `chuangmi.camera.079ac1` and `chuangmi.camera.81ac1`; C201 and EU account support are unverified. Beta reverse-engineered PTZ can move hardware. |
| [`willjayyyy/ha-xiaomi-camera`](https://github.com/willjayyyy/ha-xiaomi-camera/commit/73ce5fdef19cbbec56e8c54e332bbda2042ad411), commit `73ce5fdef19cbbec56e8c54e332bbda2042ad411` (2026-08-13; source reports integration/add-on 2.0.1). | Own code Apache-2.0. Add-on images declare amd64/aarch64; README requires 64-bit HA 2024.11+. HA OS/Supervisor flow uses both an integration and a separate add-on; setup requires adding the repository to both add-on store and HACS. HA Container needs a separately run bridge. The add-on downloads Xiaomi's closed-source native library under Xiaomi's own terms and bundles a GPL FFmpeg build; those components need separate distribution/licence review. | Provides an add-on account-link page. The documented flow signs in, then asks the user to copy the full Xiaomi redirect URL from the browser address bar back into the add-on page. Compatibility mode may ask for the Xiaomi password again and retain a long-lived credential until the add-on is uninstalled. | Camera discovery is by LAN broadcast and direct camera connections are supported. The documented transport also tries WAN hole punching and a Xiaomi relay; therefore the source does not guarantee LAN-only media. `local` mode restricts published RTSP to loopback, not necessarily every upstream camera path. README says only China account region has been tested; EU and C201 are unverified. No PTZ. |

### T01 outcome

No backend is selected. `ha-xiaomi-camera` documents the more integrated HA OS experience, while its two-part install, redirect-copy sign-in, region limitation, and closed-source SDK remain material gaps for the requested simple and supportable flow. `xiaomi-onvif` has an open MIT source path and a managed add-on in development, but requires manual go2rtc account configuration, is beta, and does not list C201 among verified models. Neither source proves that this EU C201 can authenticate, produce a frame, maintain a ten-minute stream, or recover a session.

The account inventory confirms the protocol model code `xiaomi.camera.c201`; the owner-reported label `1213911322` remains a separate value whose field meaning is unconfirmed. Backend choice remains open until a physical pilot produces a decoded frame, ten minutes of video, and session recovery. Verify PTZ independently and only with supervision.

## Evidence links

- [Xiaomi C201 support FAQ](https://www.mi.com/global/support/faq/details/KA-600067/)
- [MIoT catalog entry for `xiaomi.camera.c201`](https://home.miot-spec.com/p/xiaomi.camera.c201)
- [`xiaomi-onvif` README at reviewed commit](https://github.com/SincereXIA/xiaomi-onvif/blob/d0e26d1633a98621c7e8650e56ca81f72bfa7e69/README.md), [license](https://github.com/SincereXIA/xiaomi-onvif/blob/d0e26d1633a98621c7e8650e56ca81f72bfa7e69/LICENSE), [upstream base record](https://github.com/SincereXIA/xiaomi-onvif/blob/d0e26d1633a98621c7e8650e56ca81f72bfa7e69/UPSTREAM.md), [account/stream example](https://github.com/SincereXIA/xiaomi-onvif/blob/d0e26d1633a98621c7e8650e56ca81f72bfa7e69/examples/xiaomi-onvif/go2rtc.yaml), and [HA add-on guide/config](https://github.com/SincereXIA/xiaomi-onvif/blob/d0e26d1633a98621c7e8650e56ca81f72bfa7e69/hassio/go2rtc/DOCS.md)
- [`ha-xiaomi-camera` README at reviewed commit](https://github.com/willjayyyy/ha-xiaomi-camera/blob/73ce5fdef19cbbec56e8c54e332bbda2042ad411/README.md), [architecture/network paths](https://github.com/willjayyyy/ha-xiaomi-camera/blob/73ce5fdef19cbbec56e8c54e332bbda2042ad411/docs/ARCHITECTURE.md), [license](https://github.com/willjayyyy/ha-xiaomi-camera/blob/73ce5fdef19cbbec56e8c54e332bbda2042ad411/LICENSE), [add-on platform/config](https://github.com/willjayyyy/ha-xiaomi-camera/blob/73ce5fdef19cbbec56e8c54e332bbda2042ad411/addon/config.yaml), and [native-library build details](https://github.com/willjayyyy/ha-xiaomi-camera/blob/73ce5fdef19cbbec56e8c54e332bbda2042ad411/addon/Dockerfile)
- [go2rtc C201 support report](https://github.com/AlexxIT/go2rtc/issues/2290); this report is a lead, not a definitive compatibility test.

Compatibility status stays **unverified** until the selected backend authenticates to this owner's camera, decodes a real frame, maintains video for ten minutes, and recovers a dropped session. Record firmware and EU region with that result. PTZ is an independent test under user supervision.

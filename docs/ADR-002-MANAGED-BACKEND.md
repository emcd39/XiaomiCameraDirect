# ADR-002: Managed camera backend remains undecided

- **Status:** Candidate review complete; physical decision pending
- **Date:** 2026-11-05
- **Scope:** T01 backend evidence and product fit for the C201 pilot

## Context

The product goal is one normal Home Assistant flow for Xiaomi account setup, camera selection, video, and recovery. The first physical pilot is a C201 reported at `192.168.3.119`, firmware SDK `5.3.2_0016`, plugin `4560.8.1-8`, Xiaomi account region EU. The owner reports the number `1213911322` under the Xiaomi Home label “ID”; its meaning is not confirmed as the protocol model code. No Xiaomi credentials have been shared or entered.

T00 provides reproducible local HA/gateway/mock coverage. It does not exercise a real C201, account authentication, a live frame, or physical PTZ. The two source snapshots below are the only candidates compared for this review; facts are pinned to their exact revisions and public documentation.

## Candidates

### A. xiaomi-onvif / patched go2rtc

- **Reviewed source:** [`d0e26d1633a98621c7e8650e56ca81f72bfa7e69`](https://github.com/SincereXIA/xiaomi-onvif/commit/d0e26d1633a98621c7e8650e56ca81f72bfa7e69), dated 2026-10-30. Its upstream record pins go2rtc base [`a71f29545c8ceefa4224b201d1d34ff937300fa8`](https://github.com/AlexxIT/go2rtc/commit/a71f29545c8ceefa4224b201d1d34ff937300fa8), dated 2026-05-27.
- **Shape:** Matched go2rtc and ONVIF bridge binaries in one project image. Normal Docker use starts two services from the image. A HA add-on is documented for amd64/aarch64, but the project says the initial add-on image must be published and real HAOS validation is pending.
- **License:** Repository and recorded go2rtc base state MIT. This is a source-level review, not a full third-party software bill of materials or redistribution audit.
- **Login/configuration:** The example asks the operator to obtain a Xiaomi account ID/token through go2rtc's Xiaomi login flow and enter them with model/device/stream values. The reviewed docs do not offer a single HA-managed account flow. Secrets require protected local configuration.
- **Video/network:** Designed to open Xiaomi `miss` streams through go2rtc and expose RTSP/ONVIF locally. Docs require local network access to the camera and Internet access when Xiaomi encryption keys need refreshing. They do not establish that all media stays LAN-only under every condition.
- **Evidence/risks:** The project's tested table names only `chuangmi.camera.079ac1` and `chuangmi.camera.81ac1`. It says other `miss` cameras may work, while warning that motor IDs and preset properties differ. C201, the EU region, login, live video, and session recovery remain untested. Software is explicitly beta and PTZ can move physical hardware.

### B. ha-xiaomi-camera vendor-library bridge

- **Reviewed source:** [`73ce5fdef19cbbec56e8c54e332bbda2042ad411`](https://github.com/willjayyyy/ha-xiaomi-camera/commit/73ce5fdef19cbbec56e8c54e332bbda2042ad411), dated 2026-08-13. The integration manifest and add-on configuration at that revision report version `2.0.1`.
- **Shape/platforms:** HA integration plus a separate bridge add-on, with integration-triggered add-on installation on HA OS/Supervisor. The documented first setup adds the repository in both the add-on store and HACS. README requires 64-bit Home Assistant 2024.11+; add-on images declare amd64/aarch64. For HA Container, the bridge must be run as a separate host-network service.
- **License:** The repository's own code is Apache-2.0. Its add-on downloads Xiaomi's closed-source native streaming library under Xiaomi's own terms and includes a GPL FFmpeg build. These dependencies need an independent licence and redistribution review before bundling them in this project.
- **Login/configuration:** Account linking is presented in the add-on page. Documented flow signs in to Xiaomi, then requires copying the full redirect URL from the browser address bar back into the page. The compatibility path for models rejected by the vendor library may ask for the Xiaomi password again and retain a long-lived credential until the add-on is uninstalled.
- **Video/network:** LAN broadcast and direct camera connections are supported, with host networking required. The project's architecture also describes WAN hole punching and vendor relay fallback. Its default `local` stream-access setting binds published RTSP to loopback; it does not prove that the upstream camera media path itself never relays. The README reports only China account region tested.
- **Evidence/risks:** No C201 or EU test is documented; PTZ is not supported. This is the closer HA OS management experience of the two reviewed candidates, but it still consists of two installed components and has a redirect-copy step. HA Container setup is more manual.

More source links and the camera-specific matrix are in [COMPATIBILITY.md](COMPATIBILITY.md). Primary reviewed files: [candidate A README](https://github.com/SincereXIA/xiaomi-onvif/blob/d0e26d1633a98621c7e8650e56ca81f72bfa7e69/README.md), [candidate A add-on config](https://github.com/SincereXIA/xiaomi-onvif/blob/d0e26d1633a98621c7e8650e56ca81f72bfa7e69/hassio/go2rtc/config.yaml), [account example](https://github.com/SincereXIA/xiaomi-onvif/blob/d0e26d1633a98621c7e8650e56ca81f72bfa7e69/examples/xiaomi-onvif/go2rtc.yaml), [candidate B README](https://github.com/willjayyyy/ha-xiaomi-camera/blob/73ce5fdef19cbbec56e8c54e332bbda2042ad411/README.md), [candidate B network architecture](https://github.com/willjayyyy/ha-xiaomi-camera/blob/73ce5fdef19cbbec56e8c54e332bbda2042ad411/docs/ARCHITECTURE.md), [candidate B add-on config](https://github.com/willjayyyy/ha-xiaomi-camera/blob/73ce5fdef19cbbec56e8c54e332bbda2042ad411/addon/config.yaml), and [vendor-library build details](https://github.com/willjayyyy/ha-xiaomi-camera/blob/73ce5fdef19cbbec56e8c54e332bbda2042ad411/addon/Dockerfile).

## Decision

**Do not select or integrate either backend yet.** Both candidates lack evidence for the specific C201 firmware and EU account. Selection based on product name, community model catalog, mocks, or another camera's result would be unsupported. The physical C201 compatibility gate remains open.

This is a deliberate hold rather than a rejection. Candidate B has the more integrated HA OS experience, and candidate A has an open MIT source path with an ONVIF option; each has a material unresolved issue for this project. First prove camera identity, authentication, local video path, sustained playback, and session recovery. Reassess product fit and licence review with that evidence.

## Physical pilot gate

1. Confirm the protocol model code from the camera's Xiaomi Home account inventory. The owner's numeric “ID” value is recorded but does not confirm that code.
2. With the camera and test runtime on the same LAN, enter any account credentials only in a local candidate login UI; do not send passwords, Xiaomi tokens, cookies, or redirect URLs through chat or commit them.
3. Record backend commit/build, HA/runtime version, model code, firmware, and region. Verify account authentication and inventory, decode an actual frame, play for ten minutes, then force one recoverable session interruption and measure recovery.
4. Establish whether media uses a direct LAN path with available counters or a sanitized network observation. If relay cannot be ruled out, report that limit explicitly.
5. Test PTZ separately under supervision and only after model-specific control mapping is known. Video compatibility does not imply PTZ support.

Required before physical pilot: the exact protocol model code from the Xiaomi Home inventory and a locally operated login session in the isolated test setup. Credentials are not required in this chat. Until that gate passes, keep backend selection and C201 video status open.

## Consequences

- The managed backend interface and user flow remain provider-independent while T02 continues.
- Do not ship either upstream implementation or claim C201/EU support on the basis of this review.
- Preserve separate status for authentication, first frame, ten-minute stream, recovery, and PTZ. T00 mock success remains a test-stack result only.
- Revisit this ADR after the physical pilot or if exact upstream source/licence/platform details materially change.

## Verification record

This ADR is a source/documentation review dated 2026-11-05. No T00 tests were rerun for this documentation change. No credentials, camera media, or control requests were used.

# Managed-mode API contract

This is the provider-independent T02 contract in [the delivery plan](SIMPLE-STABLE-PLAN.md). `GET /v2/cameras`, managed identity persistence, and managed camera selection are implemented. Xiaomi account flows and managed media authorization are not implemented; proposal details below are requirements for those later steps.

## Managed runtime startup

Managed startup is an explicit `-managed` command-line mode. It starts with an empty camera list, uses `-listen` (default `127.0.0.1:8099`), and opens `/data/managed-state.json` by default; mount `/data` persistently. It bypasses the static camera config. Legacy startup remains the default and continues to require a valid config with at least one camera. Both modes currently require `XLG_API_TOKEN`; automated HA pairing is not implemented.

In managed mode, authorized `GET /v2/info` returns the persisted identity, the current managed API version, and only implemented features. The current feature list advertises persisted camera selection:

```http
GET /v2/info HTTP/1.1
Authorization: Bearer <gateway-token>
```

```json
{"instance_id":"inst_<32-hex-digits>","api_version":"2.0","features":["camera_selection"]}
```

The instance ID survives a gateway restart because it is loaded from the managed data file. Missing/invalid API authorization returns `401`; invalid or insecure state prevents startup and is not silently replaced.

## Compatibility

- Keep existing `/v1` camera and PTZ response behavior for the current integration.
- Managed setup uses `/v2`. In explicit `-managed` mode an authorized `GET /v2/info` returns persisted `instance_id`, `api_version`, and supported `features`; do not infer capabilities from a URL or a guessed build number.
- Every current API route except health/readiness checks is authorized. The planned pairing flow grants an HA config entry scoped to this runtime; camera discovery never sends a long-lived token.
- Inventory and capability fields never contain network addresses, Xiaomi account IDs, raw device IDs, tokens, cookies, or backend credentials. The current authorized `GET /v2/cameras` can additionally include credential-free RTSP endpoints for configured sources; the handler rejects userinfo, query strings, and fragments. The bearer token protects this HTTP response only. It is not forwarded by HA's RTSP media client, so the current route does **not** authenticate access to the RTSP server. Do not treat this as a production managed-stream security design or expose an unauthenticated RTSP listener to an untrusted network. T06 must provide media authorization that HA can actually use, with credential redaction and tests, before managed release.

## Identity and lifecycle

- `instance_id` is an opaque random identifier created once and persisted with runtime data. Restart, upgrade, and restoring the same installation preserve it. An intentional clone for a second live installation must rotate it before either copy is paired with HA. Changing the API address, token, host name, or port does not create a new instance.
- `camera.id` is an opaque, installation-scoped identifier stored in a runtime-owned mapping. It must not be an IP address, display name, list position, Xiaomi account ID, raw Xiaomi device ID, or token. Renaming a camera or changing its firmware, address, or network does not change the ID. Relinking the same Xiaomi account should recover the mapping. A different account must not silently inherit another account's camera identity. If the backend cannot prove continuity, require an explicit migration/re-pair decision instead of guessing.
- HA device identifiers use `(domain, camera.id)`. Entity unique IDs derive from the stable camera ID and entity role, never from the gateway URL. Inventory reordering must not rename or recreate entities. Temporary inventory, network, or authentication failures must not delete entities; removal is a separate explicit selection/removal operation.
- Current legacy identifiers are already registered from the configured camera ID: device identifier `(xiaomi_local_gateway, <camera-id>)`, camera entity unique ID `<camera-id>_camera`, and PTZ button IDs `<camera-id>_ptz_up|down|left|right`. Keep `/v1` payloads and those IDs unchanged. A managed migration must carry an explicit old-to-new mapping and preserve matching entity-registry entries; do not create duplicate entities or silently migrate by URL, display name, or address. The current config-flow entry itself is keyed by normalized gateway URL and must be migrated separately.
- A future `GET /v2/info` exposes `instance_id` and API capabilities, never a token or account identity. On first pairing HA stores the instance identity. If it changes later, HA must stop before attaching entities and explain that the endpoint now points to a different runtime.

## Camera view

The initial Go model is `model.Camera` in `internal/model/camera.go`. Its `video` and `control` states are independent. Capabilities mean **confirmed support by the selected backend for this model/firmware/region**, not merely that an upstream Xiaomi API advertises a method.

- Video states: `not_selected`, `unsupported`, `idle`, `connecting`, `streaming`, `retrying`, `offline`, `auth_required`, `privacy`, `error`.
- Control states: `unsupported`, `ready`, `auth_required`, `error`.
- `idle` means no active video request; it is not proof of a working stream. `streaming` requires a recent decoded frame. `privacy` requires an explicit device signal.
- Stable error codes include `wrong_region_or_empty_account`, `auth_required`, `unsupported_model`, `unsupported_transport`, `lan_unreachable`, `stream_timeout`, `codec_unsupported`, `backend_unavailable`, `storage_error`, and `api_incompatible`.
- Keep a stream frame timestamp distinct from the time the status was checked. A stale frame is not a fresh snapshot.

## Inventory route status

- `GET /v2/info` **implemented in explicit managed mode:** returns persisted `instance_id`, `api_version` (`2.0`), and `features: ["camera_selection"]`. It requires bearer authorization. Legacy mode does not register this route and returns `404`.
- `GET /v2/devices`: devices visible to the authenticated Xiaomi account; an empty list is valid. Unsupported devices remain visible with a reason.
- `GET /v2/cameras` **implemented:** returns `{"cameras": [...]}` with selected cameras and their stable IDs, capabilities, video state, control state, and sanitized reasons. When the provider implements `StreamProvider`, a camera may contain a `streams` list with credential-free RTSP endpoints. In managed mode with an empty saved selection and no Xiaomi provider, it returns `{"cameras":[]}`. If cameras are selected but no provider is installed, it returns `503 backend_unavailable` rather than inventing entities.
- `PUT /v2/selection` **implemented in managed mode:** replaces selected IDs with `{"revision":<current>,"camera_ids":[...]}`. A successful change increments and persists the revision; sending the same set with the current revision is idempotent. A stale revision returns `409` with the current revision and IDs. The route requires bearer authorization and is not registered in legacy mode.

The Go boundary is in `internal/backend/provider.go`: `CameraProvider` owns inventory and status, optional `StreamProvider` supplies local stream endpoints, and optional `PTZProvider` owns camera movement. The current `ConfigProvider` adapts existing static RTSP/ONVIF settings. It does not authenticate to Xiaomi and is not the managed C201 backend. The provider retains ownership of returned snapshots; consumers read them as immutable. Providers may share a snapshot across calls, but must support concurrent calls and must not mutate the slice, camera values, or timestamp pointers after returning them. The API makes its own slice and timestamp copies before normalizing a response. Video-only providers implement only `CameraProvider`. If no PTZ provider is injected, the API clears PTZ/preset capabilities and reports control as `unsupported`, even if an inventory implementation mistakenly claims support. Provider failures return a sanitized `503 backend_unavailable`; invalid camera records or stream endpoints return `502`.

### Current response example

This shape is for the implemented static provider. Values are illustrative and are not a C201 result. The `streams[].url` is included only in the authenticated API response, but the RTSP media connection itself is not authenticated by the API bearer token.

```http
GET /v2/cameras HTTP/1.1
Host: gateway.example:8099
Authorization: Bearer <gateway-token>
```

```json
{
  "cameras": [{
    "id": "front-room",
    "name": "Front room",
    "model": "Example camera",
    "selected": true,
    "capabilities": {"video": true, "snapshot": false, "audio": false, "ptz": false, "presets": false},
    "video": {"state": "idle"},
    "control": {"state": "unsupported"},
    "streams": [{"id": "main", "protocol": "rtsp", "url": "rtsp://camera.example.invalid:554/live"}]
  }]
}
```

### Optional per-camera support evidence

`GET /v2/cameras` may include an optional `support` object for clients that can display per-function diagnostics. Existing `capabilities`, `video`, and `control` fields remain unchanged and sufficient for older clients. `capabilities` states what the selected backend advertises for that camera/profile; `video` and `control` report current state; `support` explains implementation scope and evidence. Unknown or absent `support` data must not make an otherwise valid camera unusable.

```json
{
  "support": {
    "schema_version": 1,
    "features": {
      "video": {
        "implementation": "implemented",
        "validation": "device",
        "path": "camera_lan",
        "requires_cloud": ["account_auth", "inventory", "new_session_negotiation"],
        "evidence_ref": "TEST_REPORT.md#latest-c201-l2-decode-7-nov-2026",
        "tested_firmware": "5.3.2_0016"
      },
      "camera_settings_read": {
        "implementation": "implemented",
        "validation": "offline",
        "path": "cloud",
        "requires_cloud": ["account_auth", "inventory", "settings_read"],
        "evidence_ref": ".test-env/evidence/test-local-20261112-162635-f49fca83.log#late-settings"
      }
    }
  }
}
```

Schema 1 uses `implementation` values `absent`, `research`, or `implemented`; `validation` values `none`, `offline`, `device`, or `home_assistant`; and `path` values `camera_lan`, `cloud`, `nvr_local`, or `unknown`. `path` names the route declared by the implementation/code review; it is **not** proof from packet capture that all traffic followed that path or that the feature works without internet. `validation` states the evidence level, and `evidence_ref` points to the report or test log. `tested_firmware` is included only when the evidence identifies a firmware version. The `requires_cloud` list names operations such as `account_auth`, `inventory`, `new_session_negotiation`, and `settings_read`; a direct camera stream still depends on cloud authentication, inventory, and session setup, while settings reads use the cloud directly. A mock or offline test never becomes `device` evidence by itself.

The Xiaomi provider adds this metadata only to the exact C201 model profile. The API removes PTZ/preset evidence if no PTZ provider is installed, and removes settings-read evidence if no settings provider is installed. The HA client ignores unknown schema versions, unknown feature names, and malformed optional feature records while keeping the camera's existing state/capabilities. HA exposes accepted support data as the camera entity's `support` state attribute; it does not create entities for unimplemented functions.

Current HTTP errors are small and do not echo upstream details:

| HTTP | Body | Meaning |
| --- | --- | --- |
| `401` | `{"error":"unauthorized"}` | Missing or invalid gateway bearer token. |
| `400` | `{"error":"invalid_selection"}`, `{"error":"invalid_camera_id"}`, or `{"error":"duplicate_camera_id"}` | Selection request is incomplete or contains malformed/duplicate IDs. |
| `409` | `{"revision":<current>,"camera_ids":[...]}` | Selection revision is stale; no data is changed. |
| `413` | `{"error":"selection_too_large"}` | Serialized state would exceed the store size limit; prior memory and file state stay unchanged. |
| `502` | `{"error":"invalid_backend_response"}` | Provider returned an invalid camera or stream record. |
| `503` | `{"error":"backend_unavailable"}` or `{"error":"storage_error"}` | Required camera provider or storage operation is unavailable. |

For `/v2/info`, `401` is the same authorization failure. The endpoint is not registered in legacy mode (`404`). A malformed state file prevents managed startup; the runtime does not replace it or generate a fallback identity.

### Read-only C201 settings status

When the injected camera provider implements `CameraSettingsProvider`, the authenticated route `GET /v2/cameras/{id}/settings` reads only a fixed C201 property allowlist through the existing account. It accepts no property IDs and exposes no write route. Home Assistant checks the `camera_settings_read` feature in `/v2/info` in a background task and retries that check every 60 seconds after a transport error or while the feature is absent. Camera and button setup does not wait for this optional check, and the settings route is not called until the feature is advertised. Once enabled, the integration polls settings every five minutes. `night_shot` has the model-spec mapping `0=Off`, `1=On`, `2=Auto`; five diagnostic binary sensors report whether the indicator-light, motion-detection, human-detection, movement-detection, and pet-detection options are enabled. The binary sensors report configuration values, not live detection events. Unknown Night Shot integer values are shown as `Unknown (n)`; nonzero MIoT result codes make the corresponding entity unavailable.

The fixed properties are `night_shot` (`SIID 2 / PIID 3`, `uint8`), `indicator_light` (`SIID 4 / PIID 1`, `bool`), `motion_detection` (`SIID 5 / PIID 1`, `bool`), `pet_detection` (`SIID 8 / PIID 3`, `bool`), `human_detection` (`SIID 8 / PIID 6`, `bool`), and `move_detection` (`SIID 8 / PIID 7`, `bool`). These addresses and types come from the local C201 MIoT diagnostic specification. Current property values and read availability on the camera's reported firmware have not yet been verified in a current authorized run.

```http
GET /v2/cameras/<opaque-camera-id>/settings HTTP/1.1
Authorization: Bearer <gateway-token>
```

```json
{
  "observed_at": "2026-11-12T12:00:00Z",
  "properties": {
    "night_shot": {"code": 0, "value": 0},
    "indicator_light": {"code": 0, "value": true},
    "motion_detection": {"code": 0, "value": true}
  }
}
```

The provider must have loaded the camera inventory before the request; the settings read itself does not trigger discovery, open a media session, or change camera settings. A missing camera returns `404 camera_not_found`, another model returns `409 unsupported_model`, malformed MIoT output returns `502 invalid_backend_response`, and missing provider/account or upstream failure returns sanitized `503 backend_unavailable`. The response contains no Xiaomi device ID. This reports MIoT property values only; it does not prove physical IR filter movement, image quality in darkness, event delivery, or current read availability for every property.

### Proposed managed media authorization for T06

The camera entity's `stream_source()` must return a URL the HA stream pipeline can open. Home Assistant's [camera integration guidance](https://developers.home-assistant.io/docs/core/entity/camera/) uses a stream source URL, and its [Generic Camera docs](https://www.home-assistant.io/integrations/generic/) explicitly support RTSP usernames/passwords in the URL or configured separately. That makes a scoped media account a viable proposal, subject to a test against the pinned HA and gateway versions.

Proposed flow: trusted pairing provisions a random gateway-only media username/password for this HA config entry. HA stores them in private config-entry data; the runtime stores the corresponding credentials in private `/data`. The authorized camera API continues to return a credential-free endpoint. HA combines that endpoint with the paired media credentials only when returning the camera's `stream_source()`, for example:

```text
rtsp://<paired-media-user>:<paired-media-secret>@gateway.internal:8554/<opaque-camera-route>
```

The runtime must require RTSP authentication on the actual listener; an unguessable route alone is not authorization. These credentials are not Xiaomi credentials or the gateway's API token. Revoke/rotate them on unlink, config-entry removal, or explicit repair. The integration and runtime must redact userinfo from logs, diagnostics, errors, and stream state. No media credential is included in `/v2/cameras`, discovery, or an unauthenticated route. Verify URL escaping, Home Assistant's actual FFmpeg handoff, listener reachability from HA Core, and authentication behavior of the pinned RTSP server; if that stack cannot safely enforce this contract, use a tested authenticated proxy instead.

Expected media failures: missing/invalid RTSP credentials return an RTSP `401` and trigger a single repair prompt, an unknown or unselected opaque route returns RTSP `404`, and an unavailable source fails closed rather than serving a stale frame. The HA integration maps these to distinct diagnostics (`auth_required`, `not_selected`/`camera_not_found`, `backend_unavailable`, `stream_timeout`, `codec_unsupported`). Exact server wire behavior must be verified before implementation; this managed media contract is proposed, not implemented. Current `/v2/cameras` RTSP URLs remain static-provider test behavior and have no RTSP authorization.

The fake provider behavior and shared response examples are exercised against [`tests/fixtures/managed-cameras.json`](../tests/fixtures/managed-cameras.json). It covers an offline camera and an `auth_required` camera with PTZ unsupported; it does not represent physical C201 results.

Account-flow request/response JSON, selection revision behavior, explicit deletion, and the final managed media transport are owned by T03–T06. Do not implement an imagined Xiaomi login protocol in this contract.

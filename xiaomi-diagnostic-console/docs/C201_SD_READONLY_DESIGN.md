# C201 SD card discovery: read-only provider proposal

Status: F05 read-only MIoT status/capacity is implemented in the gateway and Home Assistant integration. Go and HA 2026.10.4 headless tests passed, and a physical C201 returned all four properties. Their units and meaning remain unverified. F06 recording index and F07 clip fetch remain design only. No C201 storage-channel probe, index listing, or recording download was performed.

## Evidence and limits

The local MIoT specification identifies service 3 as Memory Card Management for xiaomi.camera.c201. It lists these properties:

| SIID | PIID | Property | Format and range | Access |
|---:|---:|---|---|---|
| 3 | 1 | Status | uint8, 0–100 step 1 | read, notify |
| 3 | 2 | Storage Total Space | uint32, 0–2147483648 step 1 | read, notify |
| 3 | 3 | Storage Free Space | uint32, 0–2147483648 step 1 | read, notify |
| 3 | 4 | Storage Used Space | uint32, 0–2147483648 step 1 | read, notify |

The specification has no unit field for these properties and no enum mapping for Status. Their access lists contain read and notify, not write.

The local Xiaomi Viewer communication record contains a successful property batch for SIID 3, PIIDs 1–4. Every result has code 0; the recorded values are Status 1, total 0, free 0, used 0. The same C201 diagnostic sequence read Device Information SIID 1, PIID 4 with code 0 and firmware value 5.3.2_0016 immediately before the storage-property batch. These are dated diagnostic values, not a fresh read. The local evidence does not define the unit, status mapping, or what zero means, so it cannot establish whether a card is present or its capacity.

Xiaomi's product specification advertises MicroSD support from 8 GB to 256 GB. This is a supported range, not the installed card's size or capacity.

The Gateway account code has a context-aware read operation, Account.MiotGetContext, backed by /miotspec/prop/get. F05 now exposes only the fixed C201 storage-property allowlist through the Xiaomi provider and authenticated API. F06 has no RDT index provider/API yet. Do not use MiotSet or MIoT actions for this feature.

The C201 pilot plan reports model xiaomi.camera.c201 and firmware 5.3.2_0016. The media request's channel field is separate from a storage channel. The provider passes Channel 0 to the live media stream; do not reuse it for RDT. Protocol notes mention storage channels 0 and 10 for CW500, not C201. No local C201 RDT index capture or fixture establishes its storage channel.

## Separate conclusion by function

| Function | Confirmed for the recorded C201 session | Existing code can do | Still unknown |
|---|---|---|---|
| Card status and capacity | Readable MIoT properties SIID 3 / PIID 1–4 exist. The 2026-11-06 record has code-0 results: status 1, total/free/used 0; firmware read in the same sequence is 5.3.2_0016. | Implemented as authenticated `GET /v2/cameras/{id}/storage`, restricted to exact model `xiaomi.camera.c201`; values/codes stay raw and units stay unknown. Go and HA 2026.10.4 headless tests passed. | Units, meaning of Status 1, meaning of zero space values, current card presence, and actual total/free capacity. Preserve raw values and do not infer absence or bytes. |
| Read-only recording listing | No C201 RDT index response or storage-channel mapping was found. | Session.Recordings(channel) sends RDT IndexCommand 6 and returns sorted clip starts, durations, and event flags. It is not wired into the provider/API. | The C201 storage channel and the valid wire representation of an empty index. |
| Selecting and reading a clip | No C201 clip was fetched or played through this path. | Session.FetchRecording(start, channel) accepts the exact index start; FileRequest now encodes a long payload carrying the timestamp and channel. | Whether C201 accepts the request and returns the file on a verified channel; whether the C201 channel has been verified. |

## F05 implementation and F06/F07 proposal

The optional `CameraStorageProvider` boundary is implemented so HTTP handlers do not depend on Xiaomi protocol types. F05 provides a raw storage-property snapshot for a stable camera ID. The Xiaomi provider rejects models other than `xiaomi.camera.c201` before any storage read, uses its private device ID and existing `Account.MiotGetContext`, and allows only SIID 3 / PIIDs 1–4. The RDT index and recording-fetch provider operations remain unimplemented. Never accept a client-supplied device ID, SIID, PIID, or RDT channel.

F05 current response shape:

`GET /v2/cameras/{id}/storage` is authenticated and reads the four MIoT properties. The response contains `observed_at` and a `properties` map; each returned property has `code`, optional/raw `value`, and `unit: null`. There is no `status_interpretation` field and no converted capacity field. For example:

```json
{
  "observed_at": "<RFC3339 timestamp>",
  "properties": {
    "card_status": {"code": 0, "value": 1, "unit": null},
    "total": {"code": 0, "value": 0, "unit": null},
    "free": {"code": 0, "value": 0, "unit": null},
    "used": {"code": 0, "value": 0, "unit": null}
  }
}
```

This is an illustrative response shape with historical property values; the timestamp is a placeholder, not a captured current response. A nonzero per-property code omits its value; a missing property stays absent. Neither case is translated into “no card”, and zero is not converted to bytes. The route is advertised with `sd_capacity` only when the storage provider is available.

Proposed F06/F07 read-only routes:

- GET /v2/cameras/{id}/storage/recordings returns index state and clips with exact start epoch seconds, duration seconds, and event flag. Capacity remains unknown unless a property with documented units supplies it.
- GET /v2/cameras/{id}/storage/recordings/{start}/download accepts only an exact start returned by the current index for that camera. It must not round timestamps or accept arbitrary points within a clip. Reject stale or absent starts before calling FetchRecording.

Reuse the existing prepared C201 account/session state; do not open an additional CS2 session merely to list storage. Serialize RDT index and fetch operations per session because they share the RDT feed, and coordinate them with session retirement. Add context-aware timeouts at the protocol boundary. MIoT reads must use the existing request context and must not start background retries.

## Channel proof and response states

Do not infer the SD channel from live media Channel 0 or scan candidate channels. First require either a protocol sample/specification that explicitly links xiaomi.camera.c201 and firmware 5.3.2_0016 to the RDT IndexCommand channel, or a separately authorized, bounded read-only probe whose response command and parsed index are correlated to that exact camera. Store that mapping only in the C201 provider profile with its provenance.

Represent these cases separately:

- channel_unverified: return before sending any RDT request.
- available: a response for the verified channel has a structurally valid index with clips.
- empty_index: only a correlated command-6 reply with the protocol-verified empty-index encoding and zero clips. No clips alone do not prove that no card is installed.
- index_timeout: no matching index reply before the bounded deadline. Return a timeout error; never turn it into an empty list or no-card result.
- malformed_index: a reply arrived but its framing or entries fail validation.
- unsupported_model: reject without touching the session.

The current code treats short and zero-byte payloads as too short for ParseIndex, while no matching reply times out. Do not relabel either outcome as a valid empty list until a C201-specific empty-response format is evidenced. A channel that has not been proven remains unknown even if a guessed channel produces no reply.

Capacity stays independent of index state. Only expose a converted capacity after a read-only C201/firmware property with documented units and semantics is verified. The recorded MIoT values remain raw; Xiaomi's supported 8–256 GB range is not the installed card's capacity.

## File-request channel encoding

The FileRequest(timestamp, channel) builder now creates a 24-byte payload, writes the timestamp at bytes 0–3 and channel little-endian at bytes 8–11, then sends RDT FileCommand 1. The adjacent protocol comment says firmware reads bytes 8–11 as the channel only when the payload is longer than 19 bytes; the old 12-byte body therefore defaulted to channel 0. This change fixes the generic builder's layout, but C201 acceptance remains unverified; do not claim that the source comment proves behavior on C201 firmware 5.3.2_0016.

## Offline test plan

1. Valid index fixture: parse multiple records, sort by exact start, preserve duration/event flags, and return the same start values to the API.
2. Empty index fixture: use a captured/spec-backed command-6 response with a valid empty encoding; return empty_index with zero clips but do not claim no card. A zero-byte payload without a verified empty encoding must remain malformed/ambiguous.
3. Missing reply: fake RDT transport yields no command-6 response; assert the bounded timeout error and that the result is not empty_index.
4. Unverified channel: assert no RDT send occurs.
5. Unsupported model: assert no session call occurs.
6. Exact clip selection and file request: take a start from the index, assert FetchRecording receives the identical start, then inspect the emitted FileCommand payload. Assert its length is greater than 19, bytes 0–3 decode to the exact start, and bytes 8–11 decode to the provider-owned nonzero channel. The current builder-level Go test checks the payload length and byte positions; a future provider test must additionally prove that only an exact current-index start reaches FetchRecording. Reject rounded, altered, stale, or cross-camera starts without fetching.
7. Capacity-property fixture: parse code-0 responses for SIID 3 / PIIDs 1–4, preserve raw values and unknown units/status meaning, and assert that zeros are not translated into no-card or byte capacity.
8. MIoT read failure: nonzero per-property code, omitted property, or canceled request remains unknown/error and does not trigger a write, retry, or MIoT action.
9. Capacity without an explicit unit: assert null/unknown converted capacity regardless of values or number of clips.

The generic FileRequest builder now emits a 24-byte payload with the timestamp in bytes 0–3 and the little-endian channel in bytes 8–11. `internal/xiaomi/protocol/recordings/recordings_test.go` covers those bytes with a nonzero channel; it passed on 2026-11-12 with `C:\Apps\go\bin\go.exe test ./internal/xiaomi/protocol/recordings -count=1` (Go 1.27.3, windows/amd64). This verifies the generic payload layout only; it does not prove that C201 firmware 5.3.2_0016 accepts the extended request. The implemented F05 storage-properties endpoint does not imply an F06 RDT index endpoint or a live RDT probe.

## Reviewed local sources

- [C201 pilot model and reported firmware](../../docs/C201-STANDALONE-PLAN.md)
- [Managed Gateway routes](../../internal/httpapi/api.go)
- [Xiaomi provider and live media channel](../../internal/xiaomi/provider.go)
- [MIoT property read implementation](../../internal/xiaomi/protocol/cloud/api.go)
- [Generic RDT index and recording parser](../../internal/xiaomi/protocol/recordings/recordings.go)
- [RDT index and exact-start fetch methods](../../internal/xiaomi/protocol/stream/stream.go)
- Local diagnostic metadata reviewed without printing device identifiers: .test-env/c201-standalone/private/evidence-s01/XiaomiViewer-diagnostics-20261106-165552-37a5d3ae/miot-spec.json, communication.json, and their ZIP. The property-read batch contains raw values; no units are defined.

# Local integration test stack

For the direct HA pilot, run `python -B -m unittest discover -s tests -p "test_ha_direct_*.py"`
from the repository root, using the pinned HA image or the dependencies
in [requirements.txt](requirements.txt). The checks cover preflight, MISS
media framing and decryption without camera access. In the pinned HA image, also run
`python3 -B -m unittest discover -s tests/e2e -p test_ha_direct_media_decode.py`
for synthetic H.265 decryption and decoding with FFmpeg. See the [C201 pilot runbook](../docs/HA-DIRECT-PILOT.md)
for checking the route and CS2 discovery from the target HA network namespace.

From the repository root, run the quick camera regression in a disposable
Linux container:

```powershell
./scripts/test-camera-quick.ps1
```

On Linux with Go and Node installed, the equivalent is
`sh scripts/test-camera-quick.sh`. To build the production Linux image with
WSLc, run `wslc build --tag xlg-gateway:local .`.

The quick check runs the gateway inventory API, RTSP and console selection regressions,
and the console UI test against in-process mocks. It checks that refresh lists
unsupported cameras, opens no stream, and that only an explicit supported camera
selection starts one source. It also checks that polling never retries a failed
session. Once the image is built, the quick check runs with container
networking disabled. Use the full stack below for Home Assistant
integration, media playback and outage coverage.

For a physical C201 with the workstation's authorized Xiaomi Viewer account,
run `./scripts/test-c201-ha.ps1`. It starts a Linux gateway and a fresh HA
on a shared container network, and publishes only the HA UI on Windows
loopback. The test verifies the inventory, a single C201 RTSP source, decoded
JPEG and HLS, a ten-minute HLS soak and recovery after a gateway restart.
If media negotiation fails, the sanitized evidence includes the CS2 stage.

Run the disposable Home Assistant and gateway checks from PowerShell on the
Windows workstation. The runner builds the gateway and ONVIF mock from this
checkout, copies the current custom integration into a new HA config, verifies
its file hashes inside the running HA container, and never attaches to the
older `xlg-ha-test` stack.

## Requirements

- Windows PowerShell 5.1 or newer
- `C:\Program Files\WSL\wslc.exe`
- Network access to pull the pinned Go and Home Assistant build images when
  they are not already cached

The Go test and gateway build stages pin Go 1.27.3 and Alpine 3.24 by digest.
The ONVIF mock image is pinned to the Python 3.13 Alpine image digest in
`tests/mocks/Dockerfile`. HA 2026.10.4 is checked against its registry digest
before startup; its container image ID is also recorded in the run evidence.
Go race and vet stages rebuild without cache. The Go test context includes only
`tests/fixtures/managed-cameras.json` from the otherwise excluded `tests/` tree;
the API tests use it as a provider-independent status response fixture.

## Run

From the repository root:

```powershell
./scripts/test-local.ps1
```

The standard run executes Go race tests, `go vet`, Python gateway-client tests,
HA `check_config`, a fresh integration setup, entity checks, gateway outage and
recovery. It also checks authenticated camera-settings routing, retries after a
transient `/v2/info` failure, capability gating, and the C201 diagnostic sensor
and binary-sensor entities. The late-capability scenario creates a second real
HA config entry through its normal flow and serves a changing gateway response
from an isolated in-process HTTP mock. Include the optional ONVIF movement check with:

```powershell
./scripts/test-local.ps1 -ExerciseMockPtz
```

The runner generates random HA credentials and a gateway token under the
ignored `.test-env` directory. It does not print or copy those values into the
evidence log. The stack is removed after the run. `-KeepStack` leaves the
uniquely named containers and local credentials available for inspection; the
runner prints their names and the published loopback HA URL.

Evidence is saved in `.test-env/evidence/`. It includes command exit codes,
Home Assistant version and image ID, source hashes, and mock PTZ log rows. It
contains no Xiaomi account data and makes no requests to a physical camera.

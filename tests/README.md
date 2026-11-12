# Local integration test stack

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
recovery. Include the optional ONVIF movement check with:

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

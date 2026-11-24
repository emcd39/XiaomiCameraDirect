# Local integration test stack

Before selecting physical cameras for development, scan the inventory stored by
the local direct integration. The scanner selects the current source IP, sends
one unicast CS2 request per valid private camera IP (no broadcast or retry),
and saves a sanitized JSON report. `available` means the selected source is in the camera LAN, the
camera replied, and the stored source address matches the current route. A
routed reply outside that LAN is reported separately because the integration
currently requires a same-subnet source. Entries with missing or malformed IP
or subnet data remain in the report with an inventory status, while valid
private IPs continue through route selection and probing even if their stored
subnet is missing. A CS2 reply without a valid stored subnet never sets
`direct_usable`; missing, malformed, and public IPs are not probed.

On the Windows test host:

```powershell
& .\.test-env\ha-native-win-py314\venv\Scripts\python.exe -B scripts\ha_camera_reachability.py `
  --ha-config-dir .test-env\ha-native-win-py314\config `
  --output .test-env\ha-native-win-py314\camera-reachability.json
```

On a Linux HA host, pass its config directory, for example:
`python3 -B scripts/ha_camera_reachability.py --ha-config-dir /config --output /config/camera-reachability.json`.
The report contains camera names, models, local addresses, route choices, and
probe results; it does not include Xiaomi account credentials.

To inspect the Xiaomi sign-in flow outside Home Assistant, use the standalone
probe. It loads `account.py` by path, drives the real login implementation, and
prints every request and response with tokens/security values redacted:

```powershell
python scripts/ha_direct_login_probe.py --mock success
python scripts/ha_direct_login_probe.py --mock phone --code 123456 --region cn
python scripts/ha_direct_login_probe.py --method password --region cn --username ME
python scripts/ha_direct_login_probe.py --method qr --region cn
```

The `--mock` scenarios (`success`, `phone`, `deadend`, `captcha`) are offline and
verify the probe itself. A real run prompts for the password and prints the
identity `flag`, whether a verification continuation is available, and the
camera inventory. It does not print or store credentials. The probe needs only
the dependencies in [requirements.txt](requirements.txt); Home Assistant itself
is not required. The same applies to `test_ha_direct_account.py`: it imports
only `account.py`, so with that dependency set installed you can run it in a
plain interpreter by injecting the module under its package name first:

```powershell
python -B -c "import sys,types,importlib.util,unittest; pkgs=('custom_components','custom_components.xiaomi_direct_pilot','custom_components.xiaomi_direct_pilot.xiaomi_direct'); [(sys.modules.__setitem__(n,types.ModuleType(n)),setattr(sys.modules[n],'__path__',[])) for n in pkgs]; s=importlib.util.spec_from_file_location(pkgs[-1]+'.account','custom_components/xiaomi_direct_pilot/xiaomi_direct/account.py'); m=importlib.util.module_from_spec(s); sys.modules[s.name]=m; s.loader.exec_module(m); sys.modules[pkgs[-1]].account=m; t=__import__('tests.test_ha_direct_account',fromlist=['x']); sys.exit(0 if unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromModule(t)).wasSuccessful() else 1)"
```

The pinned HA environment runs the same file directly with `unittest discover`;
this shortcut exists only for a local interpreter without Home Assistant.

For Xiaomi Camera Direct, run `python -B -m unittest discover -s tests -p "test_ha_direct_*.py"`
from the repository root, using the pinned HA image or the dependencies
in [requirements.txt](requirements.txt). The checks cover preflight, CS2 discovery
and TCP, signed MISS setup, media framing, decryption and reopening with a loopback
camera simulator. In the pinned HA image, also run
`python3 -B -m unittest discover -s tests/e2e -p test_ha_direct_media_decode.py`
for synthetic H.265 decryption and decoding with FFmpeg. See the [C201 pilot runbook](../docs/HA-DIRECT-PILOT.md)
for checking the route and CS2 discovery from the target HA network namespace.
With Home Assistant and FFmpeg installed, run
`python -B -m unittest discover -s tests/e2e -p test_ha_direct_ha_stream.py`
to boot a disposable HA instance, decode its HLS, verify a JPEG snapshot and confirm
connection cleanup, shared transport recovery for active viewers, PTZ routing, reauthentication,
camera selection, C400 profile switching, C201 audio opt-in with HLS audio decode,
and retained entity identity.

For the D2 backend contract and gateway/direct rollback, run these targeted
checks sequentially in the same pinned Home Assistant Python environment:

```powershell
& .\.test-env\ha-native-win-py314\venv\Scripts\python.exe -B -m unittest discover -s tests -p test_backend_contract.py
& .\.test-env\ha-native-win-py314\venv\Scripts\python.exe -B -m unittest discover -s tests\e2e -p test_ha_backend_switch.py
```

The E2E boots a disposable HA instance with simulated backends, checks entity
and device identity across a switch and rollback, verifies lazy stream opening,
and confirms the gateway stream and PTZ contract after rollback. It makes no
requests to Xiaomi or a physical camera.

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

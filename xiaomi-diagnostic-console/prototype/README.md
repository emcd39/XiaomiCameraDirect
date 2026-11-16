# Xiaomi Local Gateway camera lab prototype

Release package `v0.2.20-candidate-ptz-live-results` documents the v0.2.18 Windows console and gateway builds. It demonstrates camera selection and capability-driven controls. Its mock inventory includes two C201 cameras, a video-only C400, and an unknown-model camera with explicitly advertised PTZ. Mock video and PTZ are simulations; they make no gateway or camera requests. Live PTZ is enabled only when `Start-Live.ps1 -EnablePTZ` is used, the selected gateway inventory advertises PTZ, and the selected camera has an active session.

This is a Windows lab prototype for an already configured Xiaomi Local Gateway. The console does not pair an account, start or restart the gateway, change firewall rules, or expose itself beyond loopback. It is not a production installer.

## Run the isolated demo

1. Extract the ZIP into a new writable folder.
2. Run `scripts/Start-Demo.ps1` in PowerShell.
3. Open `http://127.0.0.1:18880`.
4. Select **Mock unknown model · PTZ test** to see the generic capability path. Clicks only return `simulated`; no motor is contacted. `Mock C400 · Garden` is video-only and its PTZ step is rejected.
5. In another PowerShell window run `scripts/Test-Mock.ps1`. It checks camera selection, mock video, the video-only rejection, and four simulated directions for the explicitly PTZ-capable unknown model.

If port `18880` is busy, run `scripts/Start-Demo.ps1 -Listen 127.0.0.1:18881` and pass `-Base http://127.0.0.1:18881` to both `scripts/Test-Mock.ps1` and `scripts/Smoke-Test.ps1`.

## Connect to an existing local gateway

The gateway must already be running on this same host and reachable at `http://127.0.0.1:<port>`. The console rejects non-loopback gateway URLs. The lab launcher prints `GATEWAY_API`, `GATEWAY_RTSP_HOST`, and `RUN_ROOT`; its API port is chosen dynamically on each start.

1. Copy `config.live.example.json` to `config.live.json`.
2. Set `gateway_url` to the exact `GATEWAY_API` printed by the launcher, for example `http://127.0.0.1:51141`.
3. Set `token_file` to `RUN_ROOT/private/api-token.txt`. Convert the exact `RUN_ROOT` path to forward slashes, then append `/private/api-token.txt`. For example, if `RUN_ROOT` is `C:\camera-lab\host-run-20261115-105132`, use this valid JSON value:

   ```json
   {
     "gateway_url": "http://127.0.0.1:51141",
     "token_file": "C:/camera-lab/host-run-20261115-105132/private/api-token.txt",
     "allowed_rtsp_hosts": ["192.168.2.145"]
   }
   ```

   Replace the example port, path, and host with the values from the current launcher output. The API token file is protected for the current Windows user. Do not copy its contents into JSON, logs, or messages.
4. Set `allowed_rtsp_hosts` to the host part of `GATEWAY_RTSP_HOST`, without its port. Set `ffmpeg_path` to an installed FFmpeg executable and `record_dir` to a writable local folder or mounted share.
5. Keep `listen` on `127.0.0.1`. Use a gateway build that supports `include_streams=false` and `streams_for=<camera ID>` for inventory-only refresh and selected-camera startup. The gateway metadata read does not open camera sessions. Selecting a video camera opens only its session; later polling does not reopen a session after EOF.
6. Run `scripts/Start-Live.ps1` with PTZ disabled first. Confirm the gateway identity, camera ID, model, and capability before selecting a camera. To enable the UI PTZ path for a supervised lab check, run `scripts/Start-Live.ps1 -EnablePTZ`. A single click sends one camera-scoped step and is never automatically retried. The gateway must advertise PTZ and report an active session for that same selected camera. An API `sent` response confirms the write only; physical movement needs separate observation.

### Diagnose a disabled PTZ control

The gateway and console report separate gates. With the gateway launcher configured for the EU account, experimental models require `-EnableExperimentalProfiles`; the console launcher independently requires `-EnablePTZ`. Inspect the raw gateway capability with the current `GATEWAY_API` and token path (do not print the token):

```powershell
$gateway = 'http://127.0.0.1:<port-from-GATEWAY_API>'
$runRoot = 'C:/camera-lab/host-run-20261115-105132' # replace with this launcher's RUN_ROOT
$tokenPath = Join-Path $runRoot 'private/api-token.txt'
$headers = @{ Authorization = "Bearer $((Get-Content -Raw $tokenPath).Trim())" }
$inventory = Invoke-RestMethod "$gateway/v2/cameras?include_streams=false" -Headers $headers
$inventory.cameras | Select-Object id, model, @{n='ptz';e={$_.capabilities.ptz}}, @{n='video_state';e={$_.video.state}}, @{n='session_active';e={$_.video.session_active}}
```

Then inspect the console's camera-scoped copy and its own PTZ switch:

```powershell
$status = Invoke-RestMethod 'http://127.0.0.1:<console-port>/api/status'
$status.ptz.enabled
$status.gateway.inventory.cameras | Select-Object id, model, selected, ptz, video_state, session_active
```

`capabilities.ptz=false` in the gateway means the model profile was not resolved. For C400 and Mi 360, use the v0.2.18-profiled gateway with `-EnableExperimentalProfiles` and the configured `de` region. If gateway PTZ is true but `$status.ptz.enabled` is false, restart the console with `-EnablePTZ`. If the selected camera has `ptz=true` but `session_active=false`, the arrows are intentionally disabled until its session is active; the UI says “ČEKÁM NA AKTIVNÍ RELACI”. “PTZ NENÍ PODPOROVÁNO” is reserved for a selected camera whose advertised `ptz` value is false. After a failed media open, polling does not reopen the session automatically; explicitly refresh the camera inventory before another supervised attempt.

### Latest live candidate check — 2026-11-15

The ordinary `Start-Live.ps1 -EnablePTZ` path was exercised from a clean extraction against the v0.2.18 gateway started with `-EnableExperimentalProfiles`. The gateway restored the Xiaomi account, returned four cameras, and advertised `capabilities.ptz=true` for both C400 (`chuangmi.camera.039a04`) and Mi 360 (`chuangmi.camera.026c02`). `/api/status` reported the same camera-scoped capability in the console. Each candidate run used a loopback inventory filter that exposed only the camera being tested, so neither isolated candidate run opened C201. No C201 PTZ or settings command was sent.

The existing host demo at `127.0.0.1:63983` was also inspected without changing it. That console uses SHA-256 `51952DF7308BEAD71AA7E58860374F6588D1C952D1C9E997310FF737E9561956` (the older v0.2.3 build) and has its PTZ switch enabled. Its gateway at `127.0.0.1:55969` uses `xlg-candidate-ptz.exe`, SHA-256 `AFECBDA6801CB15E8DDD4F659C06CFF4C7C4BD5C1E38EBED8C0739D78280B247`, and returns `ptz=false` for C400 and Mi 360 (`ptz=true` for C201). The selected Mi 360 session was streaming, so the observed unsupported label comes from the gateway capability, not an inactive session. Replace the older console with this package and restart the gateway using the v0.2.18 build and `-EnableExperimentalProfiles`; do not restart a live gateway until its current HA/recording users can tolerate a session interruption.

- **C400:** the selected profile `xiaomi-c400-cs2-test-v1` negotiated CS2/TCP and opened, but the first media read timed out after about 10 seconds. The session ended, the console showed `retrying` with `session_active=false`, and no PTZ step was sent. A second explicit selection reproduced the same timeout. C400 remains experimental; the motor path cannot be exercised until an active session survives.
- **Mi 360:** the selected profile `xiaomi-mi360-cs2-test-v1` negotiated CS2/UDP and produced a live preview. One left step was sent at `2026-11-15T13:02:25.5077948Z` for camera `xiaomi_df90e225cb20322b`. The gateway logged one `0x112` write (`operation=1`) and one `0x113` reply, received 23 ms after the command. Eight preview frames were received after the response on the same MJPEG connection. The camera's overlay clock lagged host receive time by roughly 2–3 seconds; comparison of the before/after scene showed a rightward image shift consistent with a left camera pan. Only this one direction has live movement evidence; the profile remains experimental and must not be treated as a verified four-direction map.

The filtered headless run did not open C201. An earlier unfiltered setup in the same lab work opened two read-only C201 media sessions because its inventory auto-selected the sole C201; both ended at first-media EOF, with no PTZ or settings writes. The source-checkout evidence is under `.test-env/evidence/ptz-candidate-isolated-20261115/` and `.test-env/evidence/ptz-candidates-live-20261115.log`. Camera frames remain private local evidence and are not embedded in the ZIP.

Changing camera selection stops the current preview. Video and recording need RTSP readiness; PTZ does not require decoded video, but still requires an active camera session. The console is unauthenticated and must remain on loopback.

## Enable experimental profiles

Experimental model profiles are off by default. Start the gateway with the lab launcher's `-EnableExperimentalProfiles` switch, which sets `XLG_XIAOMI_PROFILE_TEST_MODE=true` for that process. To disable them, stop and restart the gateway without the switch. Restarting is required for the environment change to take effect.

The current separate Windows amd64 gateway build is `dist/xlg-gateway-v0.2.18-profiled-model-discovery.exe`, SHA-256 `A0CE07B29A34694D30291F241B4ED512E3E9D6FC20953AD91B0FE1D3E0B2B060`. From the repository root, start it with:

```powershell
.\.test-env\c201-pilot\win-gateway\start-host-gateway.ps1 `
  -ExecutablePath '.\xiaomi-diagnostic-console\dist\xlg-gateway-v0.2.18-profiled-model-discovery.exe' `
  -EnableExperimentalProfiles
```

The launcher reads the existing protected Xiaomi Viewer profile and binds its API to loopback. The v0.2.18 build compiled and, in the latest headless run, restored the account and loaded four cameras. The API token in `RUN_ROOT/private/api-token.txt` is separate from Xiaomi account credentials. Current candidate results are recorded in [Latest live candidate check](#latest-live-candidate-check--2026-11-15); do not use the older DNS-blocked status from earlier package notes.

## Add an unlisted model

PTZ cannot be inferred from a camera name, video stream, or Xiaomi inventory: Xiaomi does not supply a universal PTZ capability flag. An unknown model stays without PTZ until an operator supplies a local experimental JSON profile for the exact inventory `model_id`, confirmed region and stream settings, plus a complete direction map for a supported protocol. The current Xiaomi driver accepts only the `cs2` / `MISS` / `CS2` / `H265` session stack and the MISS `0x112` single-step protocol with four distinct operation IDs in the range 1–4. The profile is not selected automatically and is available only with experimental mode enabled.

In experimental mode only, the account inventory includes exact model IDs declared by experimental profiles even when their ID lacks `.camera.` or `.cateye.`. The provider publishes such a device only if that exact profile resolves for the configured region and declares baseline video. With experimental mode disabled or no matching profile, the unclassified device is omitted and no media source is created.

The current inventory does not return firmware. For that reason, a local PTZ profile with a non-empty `firmware_versions` allowlist is rejected with the error `local Xiaomi PTZ profile cannot restrict firmware because the current camera inventory does not expose firmware`. A profile that places PTZ only in `verified_features` is also rejected with `local Xiaomi PTZ profile cannot declare PTZ as verified because the camera inventory does not expose firmware identity`. Keep `firmware_versions` empty for an experimental baseline PTZ profile. Do not put firmware-dependent PTZ in `baseline_features`; firmware-gated PTZ remains unavailable until the gateway can read and match the device's current firmware. A profile outside the supported session stack is rejected with `local Xiaomi PTZ profile requires the provider-supported cs2/MISS/CS2/H265 session stack`.

To load a local profile file, set `XLG_XIAOMI_PROFILE_FILE` in the PowerShell process that launches the gateway, then use `-EnableExperimentalProfiles`:

```powershell
$env:XLG_XIAOMI_PROFILE_FILE = 'C:\camera-lab\profiles.json'
.\.test-env\c201-pilot\win-gateway\start-host-gateway.ps1 `
  -ExecutablePath '.\xiaomi-diagnostic-console\dist\xlg-gateway-v0.2.18-profiled-model-discovery.exe' `
  -EnableExperimentalProfiles
```

Use the profile example and restrictions in [`PTZ-PROFILES.md`](PTZ-PROFILES.md). Unsupported protocols need a separately implemented and tested provider driver; do not copy a map from a similar model without protocol evidence. Every new profile remains experimental until its identity, session behavior, four directions, and physical motion are checked independently.

## Current status and limits

- C201 has prior physical evidence for all four directions. This package does not repeat those movements.
- The console and gateway code now use camera-scoped `capabilities.ptz`, session state, and model-profile operation maps instead of a C201-only UI/provider gate. Regression tests include an unknown model whose `left` operation is `4`, distinct from C201's map.
- C400 and Mi 360 have experimental MISS maps. The gateway advertises PTZ for both when experimental mode is explicitly enabled. One Mi 360 left step produced a `0x112` write and `0x113` reply, and time-linked frames showed a scene shift consistent with left pan; other directions and firmware matching remain unverified. C400 sessions currently end on the first-media timeout, so its PTZ command has not been sent.
- Unknown profiles can use the generic path only when their exact model and protocol mapping are explicitly configured. There is no safe automatic universal PTZ protocol.
- The package's mock unknown-model step is simulation, not hardware evidence. C400 remains video-only in the mock.
- NAS recording, SD diagnostics, Frigate automation, and read-only camera settings have separate evidence and open physical acceptance items in `BUILD-MANIFEST.txt` and the repository test report.

## Update and rollback

- Treat each ZIP as immutable. Extract a new version into a new folder; do not overwrite the running one.
- Keep `config.live.json`, gateway token files, and recordings outside the release folder.
- Test the new package in mock mode first. For live use, verify the gateway address, RTSP host allowlist, camera identity, and advertised capabilities before enabling PTZ.
- To roll back, stop the new console and start the previous package with its preserved config. Do not replace gateway account data as part of a console rollback.

Binary SHA-256 values and exact test/build evidence are recorded in `BUILD-MANIFEST.txt`. The release ZIP SHA-256 is in the adjacent `.sha256.txt` receipt.

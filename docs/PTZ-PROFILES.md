# Camera PTZ profiles

The managed API and Home Assistant integration use the same camera-scoped PTZ
capability for every provider. A camera advertises PTZ only when its selected
profile declares the feature and the provider has a matching control driver.
An inventory model name, a video stream, or an `0x113` reply alone is not proof
that a camera moves in the expected direction.

## Current Xiaomi MISS/CS2 driver

The implemented driver sends one `0x112` MISS command on the already-owned
camera session. Its current operation map is `left=1`, `right=2`, `up=3`,
`down=4`. The response command is `0x113`; physical motion still needs a
separate observation for each model/firmware family.

- C201 has prior physical evidence for all four directions.
- C400 (`chuangmi.camera.039a04`) and Mi 360 (`chuangmi.camera.026c02`) now
  declare the same driver in their experimental profiles. External projects
  report this operation map for these camera families, but this gateway has
  not sent PTZ to either device. They remain hidden unless the existing
  experimental profile mode is enabled, and their unstable first-media path
  can prevent the required active session from surviving long enough to move.
  The [Xiaomi Camera Viewer project](https://github.com/lubos-hnanicek/xiaomi-camera-viewer)
  reports C400 among its model-specific support; the
  [Homebridge Xiaomi go2rtc project](https://github.com/moguennouni/homebridge-xiaomi-go2rtc)
  reports its directional mapping on `chuangmi.camera.026c02`. These are
  compatibility leads, not physical verification by this gateway.
- No other model receives this driver automatically.

The Home Assistant integration exposes the same driver twice: four `button.*_ptz_*`
entities, and a `xiaomi_direct_pilot/ptz/step` WebSocket command that the bundled
dashboard card calls from its own D-pad. Both paths go through `PTZControl`, so
both refuse to move without an active local media session and neither retries a
failed step. The card renders the pad only for camera entities whose state
carries `ptz_directions`, and dims it while `ptz_enabled` is false.

## Add an unlisted Xiaomi model

The inventory list returned by Xiaomi does not contain a PTZ-capable flag, so
the provider cannot safely infer motor support for an unknown camera. To try a
new model without changing Go code, create a local JSON file containing a full
experimental stream profile and an explicit PTZ mapping. For example:

```json
{
  "profiles": [
    {
      "id": "lab-example-camera-v1",
      "revision": 1,
      "model_id": "vendor.camera.protocol-model",
      "regions": ["de"],
      "firmware_versions": [],
      "vendor": "cs2",
      "application_protocol": "MISS",
      "media_protocol": "CS2",
      "transport": "tcp",
      "video_codec": "H265",
      "channel": "0",
      "quality": "3",
      "audio": false,
      "experimental": true,
      "baseline_features": ["video", "ptz"],
      "ptz_control": {
        "protocol": "miss-0x112-operation",
        "operations": {"left": 1, "right": 2, "up": 3, "down": 4}
      }
    }
  ]
}
```

Use the exact model ID from the authorized camera inventory. Set stream fields
from read-only diagnostics, not from the product name. The file is limited to
1 MiB, rejects unknown JSON fields and profile conflicts, and accepts only
experimental, non-auto-selected profiles. A PTZ profile must list all four
directions with distinct operation values from 1 through 4.

Xiaomi inventory normally identifies cameras by `.camera.` or `.cateye.` in
the model ID. When experimental profile mode is enabled, the provider also
asks the account inventory for the exact model IDs in its experimental
profiles. It publishes an otherwise unclassified item only when the exact
profile resolves for the configured region and declares baseline video. With
the mode disabled, or without a matching profile, the item is not surfaced as
a camera and no media source is created.

For managed Xiaomi PTZ, the profile must use the exact session stack supported
by the current opener: vendor `cs2`, application protocol `MISS`, media
protocol `CS2`, and codec `H265`. The loader rejects any local PTZ profile
whose stack differs, so it cannot advertise a control that its session opener
will refuse. The loader also rejects a PTZ profile with a non-empty
`firmware_versions` list: the current Xiaomi inventory response does not
provide firmware identity, so a version-constrained PTZ profile could not be
verified. Leave `firmware_versions` empty for an explicitly enabled lab
profile. Firmware-gated PTZ can be added after the provider can read and verify
the camera's current firmware; it must not be moved to `baseline_features` to
bypass that check. A local profile that declares PTZ only in
`verified_features` is rejected because that feature requires a confirmed
firmware identity. The error is `local Xiaomi PTZ profile cannot declare PTZ as
verified because the camera inventory does not expose firmware identity`. The
loader errors are `local Xiaomi PTZ profile requires
the provider-supported cs2/MISS/CS2/H265 session stack` for an incompatible
stack and `local Xiaomi PTZ profile cannot restrict firmware because the
current camera inventory does not expose firmware` for a firmware allowlist.

Set `XLG_XIAOMI_PROFILE_FILE` to the local file path and
`XLG_XIAOMI_PROFILE_TEST_MODE=true`, then restart the gateway. The normal
runtime stays unchanged when the file or switch is absent. Turn the feature off
by restarting without the profile file and with the test-mode switch unset or
false. Experimental profiles require a concrete matching region; a wildcard
region will not enable them.

Do not add `"ptz"` until the model's control transport and direction mapping
are supported by evidence. A new protocol, operation payload, continuous move,
zoom, presets, or a different transport needs a separate driver; this profile
file cannot send arbitrary command IDs or payloads. First test read-only
identity/session behavior, then validate one short direction at a time on the
physical device. A new or unknown camera remains video-only when the profile
does not explicitly declare a supported PTZ driver.

## Other camera providers

The existing configured RTSP/ONVIF provider remains model-independent: an
operator can declare an ONVIF PTZ endpoint for a camera that implements that
standard. Do not infer ONVIF from an open port. Managed Xiaomi CS2 profiles use
the MISS driver above; unsupported TUTK, Agora, MTP, legacy or vendor-specific
control protocols remain disabled until a separate adapter is implemented.

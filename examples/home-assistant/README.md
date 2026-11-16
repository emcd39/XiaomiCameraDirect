# Xiaomi camera dashboard

This example puts four live cameras on one Home Assistant dashboard card. Mi 360,
C201 and C400 also show four single-step PTZ controls. C400 and Outdoor AW300 need
their experimental video profiles enabled on the gateway
(`XLG_XIAOMI_PROFILE_TEST_MODE=true`).

Copy `xiaomi-live-camera.js` into Home Assistant's `config/www` folder and
register `/local/xiaomi-live-camera.js?v=1` as a **JavaScript module** under
**Settings → Dashboards → Resources**. Bump the version query when updating
the file. The module wraps the native picture-elements card and reopens a
stalled live feed on a visible dashboard, preserving its controls.

Create a separate dashboard, open its raw configuration editor and copy
`xiaomi-cameras-dashboard.yaml`. Replace the example camera and button IDs
with the IDs in your Home Assistant instance. The card uses native Home
Assistant elements and calls `button.press` on the integration's PTZ entities.
Follow the [managed API notes](../../docs/API-MANAGED.md#streams-on-demand)
for the gateway and stream path. Playback also requires a codec the browser
supports. The WSLc test used H.264; the dashboard itself does not transcode.

For the two capture buttons, add the script definitions from
`xiaomi-captures.yaml` to your existing Home Assistant scripts. Use
`script: !include xiaomi-captures.yaml` only if you do not already have a
`script:` configuration. Create `/media/xiaomi` and make sure it is writable
by Home Assistant. Files use a unique camera name and timestamp and are
available under **Media → My media → xiaomi**. If you skip this setup,
remove both `script.xiaomi_camera_*` elements from each camera tile.

Snapshots are JPEG. The recording action requests 20 seconds of MP4 with
no lookback. The final duration can vary with stream segments. These
captures are stored by Home Assistant in local media; they do not read or
change the camera's SD card. For persistent captures across container
replacements, mount a persistent volume at `/media`.

Run the dashboard reconnect regression from the repository root with
`node --test tests/test_live_camera_card.cjs`.

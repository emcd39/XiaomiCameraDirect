# Xiaomi camera dashboard

This example puts four cameras on one Home Assistant dashboard card. Mi 360
and C201 show live video and overlay four single-step PTZ controls. C400 and
Outdoor are status tiles until their media paths are verified.

Create a separate dashboard, open its raw configuration editor and copy
`xiaomi-cameras-dashboard.yaml`. Replace the example camera and button IDs
with the IDs in your Home Assistant instance. The card uses native Home
Assistant elements and calls `button.press` on the integration's PTZ entities.
Follow the [managed API notes](../../docs/API-MANAGED.md#streams-on-demand)
for the gateway and stream path. Playback also requires a codec the browser
supports. The WSLc test used H.264; the dashboard itself does not transcode.

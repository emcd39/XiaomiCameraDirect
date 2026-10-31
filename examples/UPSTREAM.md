# Connecting a test backend

For the M0/M1 phase use the upstream `SincereXIA/xiaomi-onvif` docker example:
https://github.com/SincereXIA/xiaomi-onvif/tree/main/examples/xiaomi-onvif

This is a **dependency**, not included or forked in our source tree. It uses
patched go2rtc to connect to Xiaomi cameras, publishes RTSP on 8554 and ONVIF
on 8891. Copy the upstream example and replace `CHANGE_ME` values using its
Xiaomi login instructions. Do not commit Xiaomi account token information.

The upstream examples use docker network `camera`; where Xiaomi local broadcast
discovery is required, verify real network reachability and host networking.

Configure our gateway `config.json` with the reachable RTSP/ONVIF addresses:

```json
{
  "listen": "192.168.1.20:8099",
  "cameras": [{
    "id": "garden",
    "name": "Zahrada",
    "model": "VERIFY_ACTUAL_MODEL",
    "rtsp_url": "rtsp://192.168.1.20:8554/garden",
    "onvif_url": "http://192.168.1.20:8891/onvif/ptz_service",
    "profile": "garden",
    "ptz": true
  }]
}
```

Ensure that the upstream go2rtc stream is actually named `garden` and its
ONVIF `-camera` mapping is `garden=:8891,garden`. With a different mapping,
change `profile` accordingly. Camera model and firmware must be confirmed.

The endpoint may be unauthenticated; use a restrictive LAN firewall.
`GET /v1/frigate/config` generates a base **YAML snippet**, but must be merged
with your existing Frigate configuration. The `onvif.host` must be reachable
from Frigate; `127.0.0.1` refers to Frigate itself, not Synology.

Current ONVIF adapter supports RelativeMove (one step), Stop, GotoPreset.
The preset token must exist in the upstream service. ONVIF autotracking
compatibility depends on the camera and bridge and is not guaranteed.

#!/bin/sh
set -eu

ffmpeg -nostdin -hide_banner -loglevel error \
  -f lavfi -i 'color=c=blue:s=640x360:r=5' \
  -frames:v 1 -an -c:v libx264 -profile:v baseline -preset ultrafast \
  -tune zerolatency -x264-params 'repeat-headers=1:keyint=1' \
  -f h264 -y /tmp/mock-c201.h264

/app/rtsp_mock -file /tmp/mock-c201.h264 &
rtsp_pid=$!
python -u /app/onvif_mock.py &
onvif_pid=$!
python -u /app/frigate_api_mock.py &
frigate_pid=$!
mosquitto -c /app/mosquitto.conf &
mqtt_pid=$!

stop_children() {
  kill "$rtsp_pid" "$onvif_pid" "$frigate_pid" "$mqtt_pid" 2>/dev/null || true
  wait "$rtsp_pid" "$onvif_pid" "$frigate_pid" "$mqtt_pid" 2>/dev/null || true
}
trap stop_children TERM INT

wait "$rtsp_pid"

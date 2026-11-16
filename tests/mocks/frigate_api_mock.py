"""Read-only Frigate API fixture for the isolated Home Assistant test stack."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json


CAMERAS = ("test_garden", "test_porch")
CONFIG = {
    "version": "0.17.0",
    "mqtt": {
        "enabled": True,
        "client_id": "xlg-frigate-e2e",
        "host": "xlg-mqtt",
        "port": 1883,
        "topic_prefix": "frigate",
    },
    "model": {"all_attributes": ["person"]},
    "cameras": {
        camera_id: {
            "name": camera_id,
            "friendly_name": camera_id.replace("_", " ").title(),
            "detect": {"enabled": True},
            "motion": {"threshold": 25, "contour_area": 35},
            "objects": {"track": ["person"], "filters": {"person": {"min_score": 0.5}}},
            "record": {"enabled": True},
            "snapshots": {"enabled": True},
            "mqtt": {"enabled": True},
            "zones": {},
            "audio": {"enabled_in_config": False, "listen": []},
            "onvif": {"autotracking": {"enabled": False}},
        }
        for camera_id in CAMERAS
    },
    "classification": {"custom": {}},
}
STATS = {
    "cameras": {
        camera_id: {
            "camera_fps": 0.0,
            "detection_fps": 0.0,
            "process_fps": 0.0,
            "skipped_fps": 0.0,
        }
        for camera_id in CAMERAS
    },
    "detection_fps": 0.0,
    "detectors": {},
    "service": {"storage": {}, "uptime": 1, "version": "0.17.0"},
}


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/api/version":
            body = b"0.17.0"
            content_type = "text/plain; charset=utf-8"
        elif self.path == "/api/config":
            body = json.dumps(CONFIG).encode("utf-8")
            content_type = "application/json"
        elif self.path == "/api/stats":
            body = json.dumps(STATS).encode("utf-8")
            content_type = "application/json"
        else:
            self.send_error(404)
            return

        self.send_response(200)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_args):
        pass


print("MOCK_FRIGATE_API ready=true version=0.17.0 cameras=test_garden,test_porch", flush=True)
ThreadingHTTPServer(("0.0.0.0", 5000), Handler).serve_forever()

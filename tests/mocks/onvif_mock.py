"""Small ONVIF responder used only by the isolated local test stack."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import xml.etree.ElementTree as ET

RESPONSE = b'''<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
 xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl">
 <s:Body><tptz:RelativeMoveResponse/></s:Body>
</s:Envelope>'''


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length).decode("utf-8", "replace")
        pan = ET.fromstring(body).find(".//{http://www.onvif.org/ver10/schema}PanTilt")
        if pan is None:
            movement = "no_pan_tilt"
        else:
            movement = f"pan_x={pan.get('x')} pan_y={pan.get('y')}"
        print(f"MOCK_ONVIF path={self.path} {movement}", flush=True)
        self.send_response(200)
        self.send_header("Content-Type", "application/soap+xml; charset=utf-8")
        self.send_header("Content-Length", str(len(RESPONSE)))
        self.end_headers()
        self.wfile.write(RESPONSE)

    def log_message(self, *_args):
        pass


ThreadingHTTPServer(("0.0.0.0", 8891), Handler).serve_forever()

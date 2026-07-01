#!/usr/bin/env python3
"""ontap_sim.py - a tiny mock ONTAP REST endpoint for control-plane CRUD smoke.

This is NOT a NetApp ONTAP simulator. It is a stub that answers the handful of
ONTAP REST shapes (cluster, svm, jobs) with canned success + async job objects,
so workflow steps that hit a *known* cluster address can progress far enough to
exercise the control plane instead of erroring at the transport layer.

Real pool creation provisions OCI compute and talks to the per-pool cluster IP
that only exists after provisioning, so this sim cannot make a full CreatePool
go green. It is a seam for the SVM/ONTAP-REST style operations and a building
block for fuller e2e once OCI compute is also faked.

Stdlib only - no third-party deps - so it runs anywhere python3 exists.
"""
from __future__ import annotations

import json
import os
import re
import threading
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(os.environ.get("ONTAP_SIM_PORT", "9444"))

# In-memory state so create/list/delete are mutually consistent within a run.
_LOCK = threading.Lock()
_STATE: dict[str, dict] = {"svm": {}, "job": {}}


def _job(state: str = "success") -> dict:
    jid = str(uuid.uuid4())
    job = {"uuid": jid, "state": state, "message": "simulated", "code": 0}
    _STATE["job"][jid] = job
    return {"job": {"uuid": jid, "_links": {"self": {"href": f"/api/cluster/jobs/{jid}"}}}}


class Handler(BaseHTTPRequestHandler):
    server_version = "ontap-sim/0.1"

    def _send(self, code: int, body: dict) -> None:
        payload = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, fmt, *args):  # quieter logs
        print(f"[ontap-sim] {self.command} {self.path} -> {args[1] if len(args) > 1 else ''}")

    def do_GET(self):
        path = self.path.split("?", 1)[0]
        if path in ("/", "/api/cluster"):
            return self._send(200, {"name": "ontap-sim", "version": {"full": "NetApp Release sim"}})
        m = re.match(r"^/api/cluster/jobs/([0-9a-fA-F-]+)$", path)
        if m:
            job = _STATE["job"].get(m.group(1), {"state": "success", "code": 0})
            return self._send(200, {"uuid": m.group(1), **job})
        if path == "/api/svm/svms":
            with _LOCK:
                records = list(_STATE["svm"].values())
            return self._send(200, {"records": records, "num_records": len(records)})
        return self._send(200, {"records": [], "num_records": 0})

    def do_POST(self):
        path = self.path.split("?", 1)[0]
        body = self._read_body()
        if path == "/api/svm/svms":
            svm_uuid = str(uuid.uuid4())
            with _LOCK:
                _STATE["svm"][svm_uuid] = {"uuid": svm_uuid, "name": body.get("name", "svm"), "state": "running"}
            return self._send(202, _job())
        return self._send(202, _job())

    def do_PATCH(self):
        self._read_body()
        return self._send(202, _job())

    def do_DELETE(self):
        path = self.path.split("?", 1)[0]
        m = re.match(r"^/api/svm/svms/([0-9a-fA-F-]+)$", path)
        if m:
            with _LOCK:
                _STATE["svm"].pop(m.group(1), None)
        return self._send(202, _job())

    def _read_body(self) -> dict:
        length = int(self.headers.get("Content-Length", "0") or "0")
        if not length:
            return {}
        try:
            return json.loads(self.rfile.read(length) or b"{}")
        except json.JSONDecodeError:
            return {}


def main() -> None:
    srv = ThreadingHTTPServer(("0.0.0.0", PORT), Handler)
    print(f"[ontap-sim] listening on :{PORT}")
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        srv.server_close()


if __name__ == "__main__":
    main()

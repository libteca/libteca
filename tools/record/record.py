import base64
import json
import os
import re
import threading

from mitmproxy import http

REDACTED = "[REDACTED]"
MAX_BODY = 1024 * 1024
SENSITIVE_HEADERS = {
    "authorization",
    "proxy-authorization",
    "x-emby-token",
    "x-mediabrowser-token",
    "x-mediabrowser token",
    "cookie",
    "set-cookie",
}
SENSITIVE_BODY_KEYS = {"password", "Password", "Pw", "apiKey", "api_key", "ApiKey", "AccessToken"}


def slugify(path):
    parts = re.split(r"[^A-Za-z0-9]+", path)
    slug = "-".join(p for p in parts if p).lower()
    return slug[:60] or "root"


def encode_body(content_type, raw):
    if raw is None or len(raw) == 0:
        return None, ""
    if len(raw) > MAX_BODY:
        return None, ""
    if content_type and "json" in content_type.lower():
        try:
            parsed = json.loads(raw.decode("utf-8", "replace"))
        except ValueError:
            parsed = None
        if parsed is not None:
            if isinstance(parsed, dict):
                for k in list(parsed):
                    if k in SENSITIVE_BODY_KEYS:
                        parsed[k] = REDACTED
            return parsed, ""
    return None, base64.b64encode(raw).decode("ascii")


class Recorder:
    def __init__(self):
        self.lock = threading.Lock()
        self.seq = 0
        self.face = os.environ.get("LIBTECA_RECORD_FACE", "abs")
        self.outdir = os.environ.get("LIBTECA_RECORD_DIR") or os.path.join(
            "testcorpus", self.face, "raw"
        )
        self.host_filter = os.environ.get("LIBTECA_RECORD_HOST", "")

    def response(self, flow: http.HTTPFlow) -> None:
        req, resp = flow.request, flow.response
        if req.method == "CONNECT":
            return
        if self.host_filter and self.host_filter not in req.pretty_host:
            return
        with self.lock:
            self.seq += 1
            seq = self.seq
        path = req.path.split("?", 1)[0]
        headers = {}
        for k, v in req.headers.items():
            lk = k.lower()
            if lk in ("host", "content-length"):
                continue
            headers[k] = REDACTED if lk in SENSITIVE_HEADERS else v
        req_body, req_b64 = encode_body(req.headers.get("content-type", ""), req.raw_content)
        truncated = resp.raw_content is not None and len(resp.raw_content) > MAX_BODY
        resp_body, resp_b64 = (None, "") if truncated else encode_body(
            resp.headers.get("content-type", ""), resp.raw_content
        )
        record = {
            "face": self.face,
            "seq": seq,
            "host": req.pretty_host,
            "request": {
                "method": req.method,
                "path": path,
                "query": {k: req.query.get_all(k) for k in req.query.keys()},
                "headers": headers,
                "body": req_body,
                "body_b64": req_b64,
            },
            "response": {
                "status": resp.status_code,
                "content_type": resp.headers.get("content-type", ""),
                "body": resp_body,
                "body_b64": resp_b64,
                "truncated": truncated,
            },
        }
        os.makedirs(self.outdir, exist_ok=True)
        name = "%04d_%s_%s.json" % (seq, req.method.lower(), slugify(path))
        with open(os.path.join(self.outdir, name), "w", encoding="utf-8") as f:
            json.dump(record, f, indent=1, sort_keys=True)
            f.write("\n")


addons = [Recorder()]

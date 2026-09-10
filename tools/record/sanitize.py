#!/usr/bin/env python3
import json
import os
import re
import sys

FACES = ("abs", "jellyfin")
RE_UUID = re.compile(r"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")
RE_HEXID = re.compile(r"^[0-9a-f]{24,64}$")
RE_URL = re.compile(r"https?://[^/\s\"']+")
SKIP_EXT = (".js", ".css", ".map", ".woff", ".woff2")

STATIC_SEGMENTS = {
    "login", "ping", "healthcheck", "status", "me", "libraries", "items", "session",
    "sessions", "sync", "close", "progress", "play", "file", "s", "t", "cover",
    "personalized", "listening-sessions", "api", "socket",
    "system", "info", "public", "users", "authenticatebyname", "views", "resume",
    "images", "playbackinfo", "videos", "video", "universal", "stream", "main.m3u8",
    "hls", "playing", "stopped", "shows", "nextup", "seasons", "episodes",
    "branding", "configuration", "ancestors", "audio",
}

PARENT_RULES = {
    "abs": {
        "libraries": "{libId}",
        "items": "{itemId}",
        "session": "{sessionId}",
        "progress": "{itemId}",
        "s": "{sessionId}",
        "file": "{fileId}",
    },
    "jellyfin": {
        "users": "{userId}",
        "items": "{itemId}",
        "videos": "{itemId}",
        "audio": "{itemId}",
        "shows": "{seriesId}",
    },
}

TOKEN_BODY_KEYS = ("accesstoken", "usertoken", "apikey", "api_key", "token", "x-emby-token")
TOKEN_QUERY_KEYS = ("api_key", "apikey", "token")


def load_raw(face):
    rawdir = os.path.join("testcorpus", face, "raw")
    if not os.path.isdir(rawdir):
        return []
    out = []
    for name in sorted(os.listdir(rawdir)):
        if not name.endswith(".json"):
            continue
        with open(os.path.join(rawdir, name), encoding="utf-8") as f:
            out.append(json.load(f))
    return out


def collect_username(records):
    for rec in records:
        req = rec.get("request", {})
        body = req.get("body")
        if not isinstance(body, dict):
            continue
        if req.get("path") in ("/login", "/Users/AuthenticateByName"):
            for key in ("username", "Username"):
                v = body.get(key)
                if isinstance(v, str) and v:
                    return v
    return None


class Sanitizer:
    def __init__(self, username):
        self.username = username
        self.user_map = {}
        self.counts = {"redacted": 0, "tokens": 0, "uuid": 0, "users": 0, "urls": 0}

    def value(self, v, key=None):
        if isinstance(v, dict):
            return {k: self.value(x, k) for k, x in v.items()}
        if isinstance(v, list):
            return [self.value(x, key) for x in v]
        if isinstance(v, str):
            return self.string(v, key)
        return v

    def string(self, s, key):
        if key is not None:
            kl = key.lower()
            if kl in ("password", "pw"):
                self.counts["redacted"] += 1
                return "[REDACTED]"
            if kl in ("userid", "userids"):
                if s not in self.user_map:
                    self.user_map[s] = "user_%d" % (len(self.user_map) + 1)
                self.counts["users"] += 1
                return self.user_map[s]
            if kl in TOKEN_BODY_KEYS:
                self.counts["tokens"] += 1
                return "{token}"
        if self.username and s == self.username:
            return "admin"
        n = RE_URL.sub("", s)
        if n != s:
            self.counts["urls"] += 1
            s = n
        if RE_UUID.match(s) or RE_HEXID.match(s):
            self.counts["uuid"] += 1
            return "{uuid}"
        return s

    def query(self, q):
        out = {}
        for k, vals in (q or {}).items():
            kl = k.lower()
            new_vals = []
            for v in vals:
                if kl in TOKEN_QUERY_KEYS:
                    self.counts["tokens"] += 1
                    new_vals.append("{token}")
                elif isinstance(v, str) and (RE_UUID.match(v) or RE_HEXID.match(v)):
                    new_vals.append("{id}")
                else:
                    new_vals.append(v)
            out[k] = new_vals
        return out


def rewrite_path(face, path):
    rules = PARENT_RULES[face]
    segs = [s for s in path.split("/") if s]
    out = []
    for i, s in enumerate(segs):
        if i > 0:
            prev = segs[i - 1].lower()
            if prev in rules and s.lower() not in STATIC_SEGMENTS:
                out.append(rules[prev])
                continue
        if RE_UUID.match(s) or RE_HEXID.match(s):
            out.append("{id}")
            continue
        out.append(s)
    return "/" + "/".join(out)


def flow_name(method, path, query):
    segs = [s for s in path.split("/") if s]
    static = [s for s in segs if not (s.startswith("{") and s.endswith("}"))]
    name = "_".join(static) or "root"
    name = re.sub(r"[^A-Za-z0-9_]+", "-", name).strip("-").lower() or "root"
    page = (query or {}).get("page", [""])[0]
    if page:
        name += "_page_" + page
    if method.upper() != "GET":
        name = method.lower() + "_" + name
    return name


def sanitize_face(face):
    records = load_raw(face)
    if not records:
        print("%s: no raw captures in testcorpus/%s/raw/" % (face, face))
        return
    s = Sanitizer(collect_username(records))
    outdir = os.path.join("testcorpus", face)
    os.makedirs(outdir, exist_ok=True)
    written = []
    for rec in records:
        req = rec.get("request", {})
        resp = rec.get("response", {})
        path = rewrite_path(face, req.get("path", "/"))
        if path.lower().endswith(SKIP_EXT):
            continue
        flow = flow_name(req.get("method", "GET"), path, req.get("query"))
        headers = {
            k: v
            for k, v in (req.get("headers") or {}).items()
            if k.lower() not in ("host", "origin", "referer")
        }
        fixture = {
            "face": face,
            "flow": flow,
            "seq": rec.get("seq", 0),
            "request": {
                "method": req.get("method", "GET"),
                "path": path,
                "query": s.query(req.get("query")),
                "headers": headers,
                "body": s.value(req.get("body")),
                "body_b64": req.get("body_b64", ""),
            },
            "response": {
                "status": resp.get("status", 0),
                "content_type": resp.get("content_type", ""),
                "body": s.value(resp.get("body")),
                "body_b64": resp.get("body_b64", ""),
                "truncated": resp.get("truncated", False),
            },
        }
        name = "%04d_%s.json" % (fixture["seq"], flow)
        with open(os.path.join(outdir, name), "w", encoding="utf-8") as f:
            json.dump(fixture, f, indent=1, sort_keys=True)
            f.write("\n")
        written.append(name)
    print("%s: %d raw -> %d fixtures in %s" % (face, len(records), len(written), outdir))
    for n in written:
        print("  %s" % n)
    print("  redactions: %s" % s.counts)


def main(argv):
    faces = argv[1:] or list(FACES)
    for face in faces:
        if face not in FACES:
            print("unknown face: %s" % face, file=sys.stderr)
            return 2
        sanitize_face(face)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))

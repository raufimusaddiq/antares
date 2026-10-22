#!/usr/bin/env python3
"""Capture sanitized Hermes API fixtures, or check the live API against them.

  HERMES_BASE_URL=http://hermes:8642 HERMES_API_KEY=... python3 -I scripts/hermes_contract.py capture
  HERMES_BASE_URL=http://hermes:8642 HERMES_API_KEY=... python3 -I scripts/hermes_contract.py check

The API is only reachable inside the hermes_edge network, e.g.:
  docker run --rm --network hermes_edge -e HERMES_BASE_URL=http://hermes:8642 -e HERMES_API_KEY \
    -v "$PWD":/w:ro python:3.11-slim python3 -I /w/scripts/hermes_contract.py check
(capture needs the mount writable: drop :ro.) The key is read from the environment, never printed
or written. Every number and timestamp in a fixture is fabricated; only shapes are real.

`check` is the API contract test: it fails when a status code, key set or value type differs.
"""
import json, os, re, sys, urllib.error, urllib.request
from pathlib import Path

FIX = Path(__file__).resolve().parent.parent / "docs" / "hermes" / "fixtures"
# name, method, path, auth ("owner" | "none" | "bad")
CASES = [
    ("health", "GET", "/health", "none"),
    ("health-detailed", "GET", "/health/detailed", "owner"),
    ("capabilities", "GET", "/v1/capabilities", "owner"),
    ("models", "GET", "/v1/models", "owner"),
    ("toolsets", "GET", "/v1/toolsets", "owner"),
    ("auth-none", "GET", "/v1/capabilities", "none"),
    ("auth-bad", "GET", "/v1/capabilities", "bad"),
    ("profile-prefix-unserved", "GET", "/p/testa/v1/models", "owner"),
]
TS = re.compile(r"^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d")


def call(base, key, method, path, auth):
    headers = {}
    if auth == "owner":
        headers["Authorization"] = "Bearer " + key
    elif auth == "bad":
        headers["Authorization"] = "Bearer " + "0" * 32
    req = urllib.request.Request(base + path, method=method, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=20) as r:
            return r.status, r.read()
    except urllib.error.HTTPError as e:
        return e.code, e.read()


def sanitize(v, key=None):
    """Keep shape, fabricate values: numbers -> 1/1.0, timestamps and URLs -> constants."""
    if isinstance(v, bool) or v is None:
        return v
    if isinstance(v, int):
        return 1
    if isinstance(v, float):
        return 1.0
    if isinstance(v, str):
        if TS.match(v):
            return "2026-01-01T00:00:00+00:00"
        if v.startswith("http://") or v.startswith("https://"):
            return "http://host.example:8642"
        return v
    if isinstance(v, list):
        return [sanitize(x) for x in v]
    return {k: sanitize(x, k) for k, x in v.items()}


def shape(v):
    if isinstance(v, bool):
        return "bool"
    if v is None:
        return "null"
    if isinstance(v, (int, float)):
        return "number"
    if isinstance(v, str):
        return "string"
    if isinstance(v, list):
        return [shape(v[0])] if v else []
    return {k: shape(x) for k, x in sorted(v.items())}


def parse(body):
    try:
        return json.loads(body)
    except ValueError:
        return body.decode("utf-8", "replace")


def main():
    mode = sys.argv[1] if len(sys.argv) > 1 else ""
    base = os.environ.get("HERMES_BASE_URL", "http://hermes:8642").rstrip("/")
    key = os.environ.get("HERMES_API_KEY", "")
    if mode not in ("capture", "check") or not key:
        sys.exit(__doc__)
    bad = []
    for name, method, path, auth in CASES:
        status, body = call(base, key, method, path, auth)
        data = parse(body)
        if key in json.dumps(data):
            sys.exit("refusing: response contains the API key (%s)" % name)
        if mode == "capture":
            FIX.mkdir(parents=True, exist_ok=True)
            (FIX / (name + ".status")).write_text("%d\n" % status)
            (FIX / (name + ".body.json")).write_text(json.dumps(sanitize(data), indent=2) + "\n")
            print("captured", name, status)
            continue
        want_status = int((FIX / (name + ".status")).read_text())
        want = json.loads((FIX / (name + ".body.json")).read_text())
        if status != want_status:
            bad.append("%s: status %d, fixture %d" % (name, status, want_status))
        elif shape(sanitize(data)) != shape(want):
            bad.append("%s: response shape differs from fixture" % name)
        else:
            print("ok", name)
    if bad:
        sys.exit("CONTRACT BROKEN:\n  " + "\n  ".join(bad))


main()

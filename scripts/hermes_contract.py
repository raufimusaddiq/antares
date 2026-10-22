#!/usr/bin/env python3
"""Capture sanitized Hermes API fixtures, or check the live API against them.

  HERMES_BASE_URL=http://hermes:8642 HERMES_API_KEY=... python3 -I scripts/hermes_contract.py capture
  HERMES_BASE_URL=http://hermes:8642 HERMES_API_KEY=... python3 -I scripts/hermes_contract.py check

The API is only reachable inside the hermes_edge network, e.g.:
  docker run --rm --network hermes_edge -e HERMES_BASE_URL=http://hermes:8642 -e HERMES_API_KEY \
    -v "$PWD":/w:ro python:3.11-slim python3 -I /w/scripts/hermes_contract.py check
(capture needs the mount writable: drop :ro.) The key is read from the environment, never printed
or written. Every number and timestamp in a fixture is fabricated; only shapes are real.

`check` is the API contract test. It fails when a status code differs, a key is added or removed, a
value changes type, a list changes length or an item's shape, or the per-toolset `enabled` flags in
`/v1/toolsets` differ. That check sees the profile served at the URL it calls, which is the default
profile; it says nothing about any tenant profile until a `/p/<profile>/v1/toolsets` case exists.
Other values are ignored. Captured strings are deny-by-default redacted (SAFE_KEYS). An unreachable Hermes is also a failure. Offline self-test:
  python3 -I -m unittest discover -s scripts -p 'test_*.py'
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
]
# Deny by default: a string in a fixture is replaced by "<redacted>" unless its key is listed here.
# Free text (titles, message bodies, descriptions, error text, paths a user typed) must never be
# captured into a public repo. SAFE_KEYS holds keys whose values are API-defined identifiers/enums;
# SAFE_BY_CASE holds keys that are only safe on one endpoint (e.g. `name` is a toolset name in
# /v1/toolsets, but could be a user's name elsewhere). A new endpoint starts with no safe keys.
SAFE_KEYS = frozenset("""allowed_mime_types artifact_capabilities capabilities cloud code
    developer_capabilities gateway_state host local_vps method mode model object owned_by path platform
    session_continuity_header session_key_header state status tool_execution type version""".split())
SAFE_BY_CASE = {"toolsets": frozenset({"name", "tools"}), "models": frozenset({"id", "root"})}
TS = re.compile(r"^\d{4}-\d\d-\d\d(T\d\d:\d\d:\d\d)?")


class Unreachable(Exception):
    pass


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
    except (urllib.error.URLError, OSError) as e:
        raise Unreachable("%s %s: %s" % (method, path, type(e).__name__))


def sanitize(v, safe=frozenset(), key=None):
    """Keep shape, fabricate values: numbers -> 1/1.0, timestamps and URLs -> constants, and every
    string whose key is not in `safe` -> "<redacted>" (deny by default)."""
    if isinstance(v, bool) or v is None:
        return v
    if isinstance(v, int):
        return 1
    if isinstance(v, float):
        return 1.0
    if isinstance(v, str):
        if TS.match(v):
            return "2026-01-01T00:00:00+00:00" if "T" in v else "2026-01-01"
        if v.startswith("http://") or v.startswith("https://"):
            return "http://host.example:8642"
        return v if key in safe else "<redacted>"
    if isinstance(v, list):
        return [sanitize(x, safe, key) for x in v]
    return {k: sanitize(x, safe, k) for k, x in v.items()}


def safe_for(name):
    return SAFE_KEYS | SAFE_BY_CASE.get(name, frozenset())


def shape(v):
    if isinstance(v, bool):
        return "bool"
    if v is None:
        return "null"
    if isinstance(v, int):
        return "int"
    if isinstance(v, float):
        return "float"
    if isinstance(v, str):
        return "string"
    if isinstance(v, list):
        return [shape(x) for x in v]
    return {k: shape(x) for k, x in sorted(v.items())}


def enabled_toolsets(body):
    """{name: enabled} from a /v1/toolsets body, or None when the body is not that shape."""
    try:
        return {t["name"]: t["enabled"] for t in body["data"]}
    except (KeyError, TypeError):
        return None


def describe_toolset_change(live, base):
    """Name the toolsets whose enabled flag changed, appeared or disappeared."""
    if live is None or base is None:
        return "enabled flags unreadable"
    parts = ["%s: %s -> %s" % (n, base[n], live[n]) for n in sorted(live) if n in base and live[n] != base[n]]
    parts += ["%s: added (enabled=%s)" % (n, live[n]) for n in sorted(set(live) - set(base))]
    parts += ["%s: removed" % n for n in sorted(set(base) - set(live))]
    return "; ".join(parts)


def compare(name, status, data, want_status, want):
    """Return a list of contract violations for one case (empty when it holds)."""
    if status != want_status:
        return ["%s: status %d, fixture %d" % (name, status, want_status)]
    # Name the toolset first: a list-length change would otherwise only say "shape differs".
    if name == "toolsets":
        live, base = enabled_toolsets(data), enabled_toolsets(want)
        if live != base:
            return ["toolsets: " + describe_toolset_change(live, base)]
    if shape(sanitize(data, safe_for(name))) != shape(want):
        return ["%s: response shape differs from fixture" % name]
    return []


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
        try:
            status, body = call(base, key, method, path, auth)
        except Unreachable as e:
            sys.exit("CONTRACT BROKEN: Hermes unreachable: %s" % e)
        data = parse(body)
        if key in json.dumps(data):
            sys.exit("refusing: response contains the API key (%s)" % name)
        if mode == "capture":
            FIX.mkdir(parents=True, exist_ok=True)
            (FIX / (name + ".status")).write_text("%d\n" % status)
            (FIX / (name + ".body.json")).write_text(json.dumps(sanitize(data, safe_for(name)), indent=2) + "\n")
            print("captured", name, status)
            continue
        try:
            want_status = int((FIX / (name + ".status")).read_text())
            want = json.loads((FIX / (name + ".body.json")).read_text())
        except (OSError, ValueError) as e:
            bad.append("%s: fixture missing or unreadable (%s)" % (name, type(e).__name__))
            continue
        problems = compare(name, status, data, want_status, want)
        bad.extend(problems)
        if not problems:
            print("ok", name)
    if bad:
        sys.exit("CONTRACT BROKEN:\n  " + "\n  ".join(bad))


if __name__ == "__main__":
    main()

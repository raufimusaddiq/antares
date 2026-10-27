#!/usr/bin/env bash
# hermes-route-probe.sh: read-only probe of the deployed Hermes for docs/hermes/fixtures/hermes-route-probe.json (#112).
# Records the version, image digest, unauthenticated /health body and every HTTP route the gateway registers
# (method, path, source file:line), read from the running container's own source. No key, profile or user data is read.
# Usage (on the Antares host): sudo scripts/hermes-route-probe.sh > docs/hermes/fixtures/hermes-route-probe.json
set -euo pipefail
C=${HERMES_CONTAINER:-hermes}
version=$(docker exec -u hermes "$C" hermes --version | sed -n 1p)
image=$(docker inspect -f '{{.Image}}' "$C")
health=$(docker exec "$C" sh -c 'curl -s -m 5 localhost:8642/health')
routes=$(docker exec -u hermes "$C" sh -c 'cd /opt/hermes && grep -rHnoE "\(\"(GET|POST|PUT|PATCH|DELETE)\", \"/[^\"]*\"" gateway/platforms/api_server*.py; grep -rHnoE "add_(get|post|put|patch|delete|route)\(\s*\"[^\"]+\"" gateway/platforms/api_server*.py' | sort -u)
read -r -d '' PY <<'PYEOF' || true
import json, re, sys
version, image, health = sys.argv[1], sys.argv[2], json.loads(sys.argv[3])
rows = []
for line in sys.stdin.read().splitlines():
    parts = line.split(":", 2)
    if len(parts) != 3:
        sys.exit(f"unexpected grep line (want file:line:match): {line!r}")
    f, n, rest = parts
    m = re.search(r'\("(\w+)", "([^"]*)"', rest) or re.search(r'add_(\w+)\(\s*"([^"]+)"', rest)
    if m:
        rows.append({"method": m.group(1).upper().replace("ROUTE", "*"), "path": m.group(2), "source": f"{f}:{n}"})
seen, out = set(), []
for r in sorted((r for r in rows if r["path"].startswith("/")), key=lambda r: (r["path"], r["method"])):
    if (r["method"], r["path"]) not in seen:
        seen.add((r["method"], r["path"])); out.append(r)
print(json.dumps({"hermes_version": health.get("version"), "version_line": version, "image": image, "health": health,
                  "note": "Read-only probe of the running container (scripts/hermes-route-probe.sh): unauthenticated /health and the gateway's registered HTTP routes from its own source. No key, profile or user data. /v1/capabilities and /api/model/options bodies need a profile key and are not re-captured here (see capabilities.body.json, model-options fixtures).",
                  "routes": out}, indent=2))
PYEOF
printf '%s\n' "$routes" | python3 -I -c "$PY" "$version" "$image" "$health"

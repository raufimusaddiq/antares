#!/usr/bin/env bash
# PRD §14.2 light-load benchmark (PERF-006 / PERF-ACC-004) on the #76 capacity harness.
# 3 approved accounts, 2 workers each holding one run in flight (= 2 simultaneous SSE
# streams), fake Hermes (3 s hold, 2,000-byte reply). Samples the Antares process RSS at 1 Hz.
#
#   scripts/capacity/bench.sh                 full profile: 10 min idle + 30 min load (~42 min)
#   IDLE_SECS=60 LOAD_SECS=300 scripts/capacity/bench.sh   CI profile (~7 min)
#
# Exit 0 when the light-load RSS P95 is <= LIMIT_MIB (256) and something was actually measured, 1 when the
# target is missed or nothing was measured, 2 on a harness failure (server never listened, load driver failed).
# The last stdout line is one JSON object; see docs/runbooks/capacity.md "Light-load benchmark".
set -euo pipefail

IDLE_SECS=${IDLE_SECS:-600}
LOAD_SECS=${LOAD_SECS:-1800}
SETTLE_SECS=${SETTLE_SECS:-30}
LIMIT_MIB=${LIMIT_MIB:-256}
cd "$(git rev-parse --show-toplevel)"

T=$(mktemp -d); mkdir "$T/data"
UP=''
PID=''
cleanup() {
  [ -n "$PID" ] && kill "$PID" 2>/dev/null || true
  [ -n "$UP" ] && kill "$UP" 2>/dev/null || true
  wait 2>/dev/null || true
  rm -rf "$T"
}
trap cleanup EXIT

go build -tags capacity -o "$T/capacity" ./scripts/capacity
"$T/capacity" upstream 127.0.0.1:18081 http://127.0.0.1:18081/idp >"$T/upstream.log" 2>&1 & UP=$!
ANTARES_SECRET_KEY=$(head -c32 /dev/urandom | base64)   # throwaway, never printed
export ANTARES_SECRET_KEY
export ANTARES_ADDR=127.0.0.1:18080 ANTARES_DB_PATH=$T/data/antares.db \
  ANTARES_GOOGLE_CLIENT_ID=scratch-client-id \
  ANTARES_GOOGLE_CLIENT_SECRET=fabricated-client-secret-0123456789 ANTARES_PUBLIC_ORIGIN=https://antares.scratch.test \
  ANTARES_HERMES_URL=http://127.0.0.1:18081 ANTARES_PROVISIONER_URL=http://127.0.0.1:18082 \
  ANTARES_PROVISIONER_TOKEN=fabricated-provisioner-token-0123456789abcdef \
  ANTARES_ADMIN_GOOGLE_ISSUER=http://127.0.0.1:18081/idp ANTARES_ADMIN_GOOGLE_SUB=sub-admin \
  ANTARES_ADMIN_EMAIL=sub-admin@scratch.invalid
"$T/capacity" server http://127.0.0.1:18081/idp >"$T/server.log" 2>&1 & PID=$!
# Wait (up to 30 s) for both listeners; a slow start is a harness failure (exit 2), not a missed target.
fail_harness() {
  echo "bench: $1" >&2
  tail -n 20 "$T/server.log" "$T/upstream.log" >&2 2>/dev/null || true
  jq -nc --arg e "$1" '{error:$e, pass:false}'
  exit 2
}
for port in 18081 18080; do
  for i in $(seq 1 60); do
    (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null && break
    kill -0 "$PID" 2>/dev/null || fail_harness "scratch Antares exited before listening (see server log above)"
    [ "$i" -lt 60 ] || fail_harness "nothing listening on 127.0.0.1:$port after 30s"
    sleep 0.5
  done
done
curl -fs "localhost:18081/_ctl?hold_ms=3000&reply_bytes=2000" >/dev/null
L=("$T/capacity" load -state "$T/state.json" -pid "$PID")

"${L[@]}" -n 3 setup >&2
sleep "$SETTLE_SECS"
"${L[@]}" -secs "$IDLE_SECS" idle >"$T/idle.json"
"${L[@]}" -n 2 -secs "$LOAD_SECS" load >"$T/load.json" 2>"$T/load.err" || { cat "$T/load.err" >&2; exit 2; }

jq -n --slurpfile i "$T/idle.json" --slurpfile l "$T/load.json" --argjson lim "$LIMIT_MIB" \
  --arg go "$(go env GOVERSION)" --arg rev "$(git rev-parse --short HEAD)" \
  --argjson idle "$IDLE_SECS" --argjson loadsecs "$LOAD_SECS" '
  ($i[0].idle) as $a | ($l[0].load) as $b |
  {rev:$rev, go:$go, limitMiB:$lim, idleSecs:$idle, loadSecs:$loadsecs,
   idle:{rssMeanMiB:$a.RSSMeanMiB, rssP95MiB:$a.RSSP95MiB, rssMaxMiB:$a.RSSMaxMiB, cpuPct:$a.cpuPercentOfOneCore},
   load:{runsOK:$b.runsOK, runsFailed:$b.runsFailed, latencyP50:$b.latencyP50, latencyP95:$b.latencyP95,
         rssMeanMiB:$b.server.RSSMeanMiB, rssP95MiB:$b.server.RSSP95MiB, rssMaxMiB:$b.server.RSSMaxMiB,
         cpuPct:$b.server.cpuPercentOfOneCore, fdsMax:$b.server.FDsMax},
   pass: ($b.runsOK > 0 and $b.runsFailed == 0 and $b.server.samples > 0
          and $b.server.RSSP95MiB > 0 and $b.server.RSSP95MiB <= $lim)}' | tee "$T/result.json"
jq -e '.pass' "$T/result.json" >/dev/null

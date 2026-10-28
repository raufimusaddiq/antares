#!/usr/bin/env bash
# Phase 1 closing gate (issue #24): everything the definition of done asks for,
# in one run, with the evidence on stdout. Paste the output into the PR.
#
#   scripts/phase1-gate.sh
#
# Optional, to turn UNVERIFIED items into verified ones:
#   PLAYWRIGHT_CORE=/path/to/node_modules/playwright-core   real-browser desktop/mobile + scroll checks
#   ANTARES_LIVE_HERMES_URL ANTARES_LIVE_PROFILE_A/B ANTARES_LIVE_KEY_A/B   live cross-user memory test
#
# Exit status: 0 = everything verified; 1 = something failed; 2 = nothing failed
# but at least one item could not be verified here (listed at the end).
set -uo pipefail
cd "$(dirname "$0")/.."
failed=0
unverified=()

step() { printf '\n==== %s ====\n' "$1"; }
run() { "$@" || { echo "FAILED: $*"; failed=1; }; }

step "go version, build, vet"
go version
run go build ./...
run go vet ./...

step "Phase 1 definition of done (real HTTP stack, fake Hermes that records every request)"
run go test -count=1 -v -run 'TestPhase1Gate' ./internal/chat

step "private memory scoping and cross-user negative tests (Antares side; fake Hermes keyed by profile)"
run go test -count=1 -v -run 'SessionKeyIsPerUser|MemoryOfOneUser|WrongProfileKey|SimultaneousStreams|TwoUsersChatPrivately|ClientSuppliedProfile|HistoryIsOwnerScoped' ./internal/chat

step "history: 500-turn conversation, cursor paging, no duplicates or gaps"
run go test -count=1 -v -run 'TestHistory|TestChatListPages' ./internal/chat
echo
echo "-- server-side measurement (the command is: go test -run Measure500 -v -count=1 ./internal/chat)"
run go test -count=1 -v -run 'TestMeasureOpenAndPageBack500TurnChat' ./internal/chat

step "whole Go suite with the race detector"
run go test -race -count=1 ./...

step "web: typecheck, unit tests, build, committed bundle is current"
( cd web && run npm ci --silent && run npm run --silent typecheck && run npm test --silent && run npm run --silent build )
if [ -n "$(git status --porcelain -- internal/webui)" ]; then
  git status --short -- internal/webui
  echo "FAILED: internal/webui/dist is not the bundle web/ builds (commit the rebuilt bundle)"; failed=1
else
  echo "committed bundle matches a fresh build"
fi

step "real browser: phone and desktop layout, starting card, image attach"
if [ -n "${PLAYWRIGHT_CORE:-}" ]; then
  run node scripts/browser-check.mjs
else
  echo "UNVERIFIED: PLAYWRIGHT_CORE is not set, so no browser ran"
  unverified+=("desktop/mobile layout and scroll behaviour in a real browser")
fi

step "live container: memory from user A is not readable by user B"
if [ -n "${ANTARES_LIVE_HERMES_URL:-}" ]; then
  run go test -tags livehermes -count=1 -v -run 'TestLive' ./internal/hermes
else
  echo "UNVERIFIED: no live Hermes profiles were provided. AGENTS.md does not let a coding agent create"
  echo "profiles or write memory in the production container for this ticket; Phase 0 also recorded that"
  echo "non-default profile routes did not resolve. See docs/runbooks/memory-scoping.md."
  unverified+=("cross-user memory isolation against the live Hermes container")
fi

step "result"
if [ "$failed" -ne 0 ]; then echo "GATE FAILED"; exit 1; fi
if [ "${#unverified[@]}" -gt 0 ]; then
  echo "Nothing failed. NOT verified in this run:"
  printf '  - %s\n' "${unverified[@]}"
  echo "Phase 0 remains FAIL (docs/hermes/PHASE0-FINDINGS.md): Antares is for one trusted user, not for mutually untrusted users."
  exit 2
fi
echo "GATE PASSED (every item verified in this run)"
echo "Phase 0 remains FAIL unless its own open items have been re-run: see docs/hermes/PHASE0-FINDINGS.md."

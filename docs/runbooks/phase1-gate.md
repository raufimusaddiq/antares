# Runbook — Phase 1 closing gate (issue #24)

One command, evidence on stdout, paste into the PR:

```
scripts/phase1-gate.sh            # exit 0 verified, 1 failed, 2 nothing failed but something is UNVERIFIED
```

| DoD item (PRD §16) | Proven by | Notes |
|---|---|---|
| pending account cannot create any Hermes workload | `TestPhase1Gate/1` | real HTTP stack; fake Hermes records every request; 0 requests |
| approved-but-unprovisioned cannot chat | `TestPhase1Gate/2` | UNPROVISIONED, CONFIGURING, FAILED, DISABLED, READY-without-binding |
| two approved users chat privately | `TestPhase1Gate/3` + `memory_test.go` | own profile/key/session key on every Hermes call; cross-user probes 404 |
| revoked user loses access immediately | `TestPhase1Gate/4` | admin API suspend mid-run: next call refused, open SSE closed, 0 Hermes requests |
| UI works on desktop and mobile | `scripts/browser-check.mjs` (+ `TestPhase1Gate/5` for the shell) | needs `PLAYWRIGHT_CORE`; otherwise reported UNVERIFIED |
| no Hermes credential reachable from a browser | `TestPhase1Gate/6` | every response, SSE stream, log line and embedded UI file scanned for keys, sealed blobs, profile names, Hermes URL |

`scripts/browser-check.mjs` drives headless Chromium against the built bundle served with the Go server's own CSP and a
mocked `/api`. It needs `playwright-core` and a Chromium, which are deliberately not dependencies of `web/` and are not run
in CI. It tests the UI contract, not the Go backend.

What the gate does **not** show: that Hermes isolates one profile's memory or tools from another's (Phase 0 FAIL stands —
`docs/runbooks/memory-scoping.md`), anything about load/RAM (`PERF-006`), and anything that needs the real Hermes wire
shapes (still documented, not captured: `docs/runbooks/chat-runs.md`).

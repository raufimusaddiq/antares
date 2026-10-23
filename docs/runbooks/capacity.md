# Runbook — capacity (the recorded resource envelope)

PRD Phase 3 DoD: "measured resource envelope recorded" (§14.2, `PERF-ACC-004`). Every number below was
**measured** unless the word *extrapolated* is next to it. Nothing here was estimated from a spec, and nothing
was run against the live deployment.

## Scope: what this envelope is, and is not

- It is the cost of **the Antares Go process** (RSS, CPU, file descriptors) and of **its SQLite file**.
  It excludes Hermes, the provisioning helper, Caddy and the browser.
- Hermes, the provisioning helper and Google were **fabricated** (`scripts/capacity/upstream.go`, adapted from
  `internal/smoke/stack_test.go`): a fake Hermes streams a 2,000-byte reply in 8 `assistant.delta` chunks over a
  3 s hold, then `run.completed`. Real Hermes event streams (tool progress, larger or more frequent frames) are
  **not** measured; expect the per-run figures to be a floor, not a ceiling.
- It says nothing about isolation. Phase 0 has not passed (`docs/hermes/PHASE0-FINDINGS.md`); resource headroom is
  not a security statement.
- Web Push was not configured (no VAPID keys), no browser or PWA was involved, no OpenUI/media payloads.

## Machine and build

| | |
|---|---|
| Host | 2 vCPU (AMD EPYC 9754 guest), 7.6 GiB RAM, Linux 6.8.0-101-generic |
| Shared | the host also runs the live Hermes container and the provisioning helper; the fake upstream and the load driver ran on the **same** 2 vCPU. Host load average was 1.3–2.0 during the runs. CPU figures are therefore the Antares process's own CPU-seconds, not host utilisation |
| Go | go1.27.3 linux/amd64, commit `be9e8eb` (`origin/main` at the time) |
| Binary | `scripts/capacity` (build tag `capacity`), **not** the release image. It wires the same packages in the same way as `cmd/antares/main.go` (same `http.Server` timeouts, same embedded bundle, same SQLite pragmas: WAL, `synchronous=NORMAL`) with one difference: the OIDC endpoints point at the fake provider, because the production binary hardcodes Google and a headless run cannot complete that flow. The binary also contains the fake upstream and driver code, which sits unused in the page cache of the server process |
| Limits | production defaults (30 runs/min, 60 messages/min, 4 SSE connections per account; 4 active runs per user) |
| Date | 2026-10-23 |

## How it was measured

> **Warning: this harness copies the wiring of `cmd/antares/main.go`; nothing links the two.** `scripts/capacity/server.go`
> is a re-implementation, so a change to production wiring (a new store, a new goroutine, new middleware, a
> different `http.Server` setting) silently invalidates every number below until the harness is updated and the
> envelope re-measured. When `cmd/antares/main.go` changes in a way that affects memory, CPU or the schema, re-run.

Three processes, all on loopback with non-default ports (18080 Antares, 18081 fake IdP + Hermes, 18082 fake
provisioner), each with a throwaway data directory:

1. `capacity upstream` — fake Google OIDC provider, provisioner and Hermes. Its memory is not counted.
2. `capacity server` — the scratch Antares. **This is the only process that is sampled.**
3. `capacity load` — the driver. Everything goes through the public HTTP API: real OIDC sign-in (authorization
   code + PKCE), admin approval, tenant provisioning to `READY` (all six verification checks), `POST /api/chats`,
   `POST /api/chats/{id}/messages`, `GET /api/runs/{id}/events` (SSE). The database is never touched directly.

Sampling: `/proc/<pid>/status` (`VmRSS`, `Threads`), `/proc/<pid>/stat` (`utime+stime`, `CLK_TCK=100`) and
`/proc/<pid>/fd`, once per second. CPU % is CPU-seconds divided by wall seconds, as a percentage of **one** core
(200 % is both cores). Database sizes are `stat` of the file after a **clean shutdown** (WAL checkpointed), so
they are the on-disk size, not the size plus pending WAL.

Reproduce (run from the repo root; the key is generated, the rest is fabricated). The block is self-contained and
was executed as written (#90); it takes about 25 minutes and removes everything it started:

```sh
T=$(mktemp -d); mkdir "$T/data"
go build -tags capacity -o "$T/capacity" ./scripts/capacity
"$T/capacity" upstream 127.0.0.1:18081 http://127.0.0.1:18081/idp & UP=$!   # also listens on :18082
export ANTARES_ADDR=127.0.0.1:18080 ANTARES_DB_PATH=$T/data/antares.db \
  ANTARES_SECRET_KEY=$(head -c32 /dev/urandom | base64) ANTARES_GOOGLE_CLIENT_ID=scratch-client-id \
  ANTARES_GOOGLE_CLIENT_SECRET=fabricated-client-secret-0123456789 ANTARES_PUBLIC_ORIGIN=https://antares.scratch.test \
  ANTARES_HERMES_URL=http://127.0.0.1:18081 ANTARES_PROVISIONER_URL=http://127.0.0.1:18082 \
  ANTARES_PROVISIONER_TOKEN=fabricated-provisioner-token-0123456789abcdef \
  ANTARES_ADMIN_GOOGLE_ISSUER=http://127.0.0.1:18081/idp ANTARES_ADMIN_GOOGLE_SUB=sub-admin \
  ANTARES_ADMIN_EMAIL=sub-admin@scratch.invalid
"$T/capacity" server http://127.0.0.1:18081/idp & PID=$!
sleep 3
curl -s "localhost:18081/_ctl?hold_ms=3000&reply_bytes=2000"               # run duration and reply size
"$T/capacity" load -state "$T/state.json" -n 100 setup                                            # sign in + approve + provision 100 accounts
"$T/capacity" load -state "$T/state.json" -pid $PID -secs 120 idle
"$T/capacity" load -state "$T/state.json" -pid $PID -n 50 -secs 420 load
curl -s "localhost:18081/_ctl?hold_ms=2500&reply_bytes=2000"
"$T/capacity" load -state "$T/state.json" -pid $PID -n 100 -chats 10 -msgs 10 fill
"$T/capacity" load -state "$T/state.json" -pid $PID -n 100 search                                 # repeat until a pass finds nothing stale
curl -s "localhost:18081/_ctl?hold_ms=240000&reply_bytes=2000"             # held runs for the streams phase
"$T/capacity" load -state "$T/state.json" -pid $PID -n 100 -rpu 1 -extra 3 streams
kill $PID; wait $PID; "$T/capacity" load -state "$T/state.json" -db "$ANTARES_DB_PATH" db          # clean stop: WAL checkpointed
kill $UP; rm -f "$T"/data/antares.db* "$T/capacity" "$T/state.json"; rmdir "$T/data" "$T"
```

The block runs every phase in one server process, which proves the commands and the shape of the results. The
tables below restarted the server (and waited 30 s) before each window, because Go keeps freed pages: RSS read
late in a long single process includes the high-water mark of earlier phases. To compare with them, restart
`server` between phases. The `search` line indexes at most 3 chats per user per call; repeat it to index all 10.

`GODEBUG=gctrace=1` on the server (no code change) gave the live-heap figures below.

## 1. Idle

| State | RSS | CPU | FDs / threads | Method |
|---|---|---|---|---|
| Fresh empty DB (bootstrap admin row only) | **17.1 MiB** mean (max 17.1) | 0.00 % (below the 1/100 s tick over the window) | 15 / 7 | waited 60 s after start, then 120 s at 1 Hz (122 samples) |
| 101 accounts, all tenants `READY`, process restarted, 30 s settle | **15.6–15.8 MiB** | not sampled | 13 / 7–8 | `settledRSS`: median of 5 one-second reads, taken at the start of three `streams` runs |
| Same process right after provisioning the 100 accounts (no restart) | **28.5 MiB** mean | 0.017 % | 21 / 13 | 30 s settle, then 120 s at 1 Hz. The burst is not returned to the OS by the Go runtime; restart value above is the true resting state |

Idle cost is a rounding error: ~16–17 MiB and no measurable CPU. The tenant sweep (every 10 s) and the SSE
heartbeat are invisible at this resolution.

## 2. Under concurrent chat load

Method: N workers, each a distinct account, each holding **exactly one run in flight**: send a message, read its
SSE stream to `run.completed`, immediately send the next. Real requests through the real handlers into the store.
Hold 3 s, 2,000-byte reply, ~190-byte prompt. Zero failed runs in every row. Run latency P50 ≈ 3.02 s, P95 ≤ 3.22 s
(the 3 s hold plus ≤ 0.22 s of Antares).

Server was restarted and settled 30 s before each 60 s window (short window), and for 420 s for the steady window.

| N concurrent runs | Window | Runs done | Rate | RSS mean / P95 / max (MiB) | CPU (% of one core) | FDs max | Live heap after GC |
|---|---|---|---|---|---|---|---|
| 10 | 60 s | 200 | 3.3/s | 26.6 / 29.6 / 29.8 | 1.95 | 41 | — |
| 50 | 60 s | 1,000 | 16.7/s | 40.0 / 46.5 / 46.8 | 7.55 | 123 | — |
| 100 | 60 s | 2,000 | 33/s | 55.0 / 67.0 / 68.5 | 15.2 | 221 | — |
| 10 | 420 s | 1,400 | 3.3/s | 40.0 / 46.8 / 48.3 | 2.3 | — | 7 MB |
| 50 | 420 s | 6,950 | 16.5/s | 77.7 / 103.8 / 105.5 | 12.6 | — | 33 MB |
| 100 | 420 s | 16,449 | 39/s | 136.7 / 196.4 / 199.3 | 31.2 | 225 | 78 MB |
| 100 | 620 s (repeat, `gctrace`) | — | — | plateau 196–199, flat from ~330 s | — | — | 78 MB, flat 400–620 s |

**Read the 60 s rows as a floor.** RSS keeps climbing for about five minutes and then stops. Cause (verified in
the code and the GC trace): a finished run's event journal is kept for `retainFinished = 5 * time.Minute`
(`internal/chat/live.go`) so a reconnecting browser can resume. Live heap therefore tracks *runs completed in the
last five minutes*, at about **6.7 KB per retained run** (33 MB ÷ 4,950 runs; 78 MB ÷ 11,700 runs). It is bounded,
not a leak: the 100-concurrent heap was flat at 78 MB across the last 220 s, and RSS did not fall at idle only
because Go keeps freed pages for a while (RSS ≈ 2–3× live heap).

The load is deliberately saturating: N=100 is 39 runs/s, far above what 100 humans produce (a person sends a
message every tens of seconds, not every 3 s). So the steady rows are an upper bound for that many concurrent
accounts, and the PRD light-load target (P95 ≤ 256 MiB) is met even there (peak 199 MiB, fake-upstream caveat above).

Search indexing (see §3) is the other transient: one `GET /api/search/chats` per user across 100 users took
2.0–2.3 s total, 64 % of a core, RSS max 34–38 MiB.

## 3. Database size

Fresh DB, **100 accounts, 1,000 chats (100 users × 10 chats), 10,000 runs of one message pair each = 20,000
turns** (prompt ≈ 190 B, reply 2,000 B), no other data. Sizes after a clean shutdown:

| Stage | `antares.db` | Delta | Per run (pair) |
|---|---|---|---|
| 100 accounts `READY`, 0 chats | 0.60 MB | | |
| after 10,000 runs, no search ever used | **6.96 MB** | +6.36 MB | **636 B** |
| after every chat was indexed by search | **61.75 MB** | +54.79 MB | **+5.48 KB** |

Notes on how that was read:

- Antares stores message *metadata* only (`messages` has no content column). The big cost is the **derived search
  index** (`search_docs`, `body` + case-folded copy of every turn). It is built lazily and **only for chats a user
  searches**, and a search request indexes at most 3 stale chats (`backfillPerSearch`). So size depends on whether
  people search: 8.9× larger here when they all did. I confirmed the index was complete by running further
  search passes until one found nothing to do (0.5 s) and by the first pass alone (3 chats/user = 3,000 pairs) adding
  the same 5.48 KB per pair as the whole (16.45 MB ÷ 3,000).
- Index cost scales with text, not with run count: 5.48 KB for 2,190 B of text per pair is ≈ 2.5× the text. A 4 KB
  average reply would be ≈ 10.5 KB indexed per pair (extrapolated from the ratio).
- Two earlier, non-fresh databases gave the same unindexed rate (2,000 runs → +1.18 MB = 590 B/run).
- WAL: while running the `-wal` file sat at 4–10 MB (SQLite auto-checkpoint) and was gone after a clean stop. Budget
  **~10 MB** of additional disk on top of the main file; it is constant, not growing.
- Peak size **at the stated count**: 61.75 MB for 1,000 chats / 10,000 runs / 20,000 turns fully indexed
  (+ up to ~10 MB WAL while running); 6.96 MB if nobody searches.

## 4. Cost of an open SSE stream

Method: the fake Hermes holds each run for 240 s. Server restarted and settled 30 s. RSS taken as the median of five
1 s reads (a) before any run, (b) with the runs created but **no** client stream, (c) after opening one client
stream per run, (d) after opening three more per run. Users were fresh for each measurement so the 4-active-run cap
was not hit.

| Case | Measured | Per unit |
|---|---|---|
| 100 runs created, no client stream (b − a) | 15.6 → 32.4 MiB | 0.17 MiB per in-flight run |
| 100 runs created, no client stream, another start | 15.8 → 30.5 MiB | 0.15 MiB |
| 200 runs (2 per user), no client stream | 15.7 → 36.9 MiB | 0.11 MiB |
| first client stream on each run (c − b), 100 runs | 32.4 → 35.3 MiB | **≈ 29 KiB** per stream |
| same, second start | 30.5 → 33.5 MiB | ≈ 30 KiB |
| same, 200 runs | 36.9 → 44.9 MiB | ≈ 41 KiB |
| 3 **extra** streams on each of 100 runs (d − c), 300 streams | 33.5 → 46.9 MiB | **≈ 46 KiB** per stream |

File descriptors: 13 idle → 217 with one stream on each of 100 runs (**2 per run**: browser connection plus the
upstream Hermes stream), and 519 with 300 extra streams (**1 per extra stream**). Threads: 7 → 11–13. Per-account
cap is 4 SSE connections and 4 active runs, so one account can hold at most ~4 streams.

Interpretation: a stream is measurable but small, **30–46 KiB each**; the cost of a *run* (~0.1–0.17 MiB in flight,
plus ~6.7 KB retained for 5 minutes afterwards) is larger than the cost of watching it. Resolution is limited:
RSS is page-granular and the five-read median moves by about ±1 MiB, so at 100 streams the per-stream figure is
good to roughly ±10 KiB. Per-stream CPU was not measured separately; it is inside the §2 CPU figures. The 15 s
heartbeat is the only per-stream periodic work.

## 5. Disk growth per day

Measured inputs (§3): **636 B per run** unindexed, **6.12 KB per run** (636 B + 5.48 KB) once its chat is
searched, for ≈ 2.2 KB of text per pair. WAL ≈ 10 MB constant.

Worked example (**extrapolated**, the user numbers are an assumption, not a measurement): 50 active users × 40
messages/day = 2,000 runs/day.

| | Arithmetic | Per day | Per year |
|---|---|---|---|
| Nobody searches | 2,000 × 636 B = 1,272,000 B | **1.27 MB** | 464 MB |
| Every chat searched | 2,000 × 6,116 B = 12,232,000 B | **12.2 MB** | 4.46 GB |

Ceiling at the measured saturated rate (**extrapolated**): the fill ran 10,000 runs in 261.5 s = 38.2 runs/s, and the
DB grew 6.36 MB in that time = 24.3 KB/s unindexed. × 86,400 s = **2.1 GB/day**. No real deployment sustains
that; it is the number to compare when deciding where a disk alarm belongs. Not modelled: `audit_log`, sessions,
push rows, and user-specific text lengths. The database never shrinks on its own; retention is a product decision
this runbook does not make.

## What would change these numbers

Larger or more frequent real Hermes frames, tool-progress events, OpenUI payloads, real transcripts longer than
2 KB, Web Push delivery, and browsers that reconnect. Re-run the commands above with `reply_bytes` and `hold_ms`
set to the real figures, and put the new table beside this one with its own date.

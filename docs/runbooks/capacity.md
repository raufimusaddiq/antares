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

## 6. Light-load benchmark against the 256 MiB target (PRD §14.2, `PERF-006`, #86)

One command, one JSON result, exit code 1 if the target is missed:

```sh
IDLE_SECS=10 LOAD_SECS=30 SETTLE_SECS=5 scripts/capacity/bench.sh    # smoke profile, ~1.5 min: what is tabled below
scripts/capacity/bench.sh                                            # the §14.2 profile: 10 min idle + 30 min load, ~42 min
IDLE_SECS=60 LOAD_SECS=300 scripts/capacity/bench.sh                 # the CI profile (job `bench` in .github/workflows/ci.yml)
```

It builds the harness, starts the fake upstream and the scratch Antares (same wiring and limits as above), signs in
and provisions **3 accounts**, then runs **2 workers**, each holding exactly one run in flight (= 2 simultaneous
SSE streams, the §14.2 light-load shape), 3 s hold, 2,000-byte reply, and samples the Antares process RSS at 1 Hz.

**Measured on this host** (2 vCPU AMD EPYC 9754 guest, 7.6 GiB, Linux 6.8.0-101-generic, go1.27.3, commit `3ebf0a8`,
2026-10-23; shared with the live Hermes container, as in the Machine table). **Smoke profile only: 30 s of load, not 30 minutes.**

| Metric | Target (§14.2) | Run 1 | Run 2 | Met? |
|---|---|---|---|---|
| Commit measured at | | `3ebf0a8` | `3ebf0a8` | |
| Light-load RSS P95 | ≤ 256 MiB | **23.8 MiB** | **24.1 MiB** | yes, ~10x headroom |
| Light-load RSS peak | reported | **23.8 MiB** | **24.1 MiB** | n/a |
| Idle RSS (10 s window) | recorded | 21.2 MiB | 21.6 MiB | n/a |
| Runs completed / failed | no errors | 20 / 0 | 20 / 0 | yes |
| Run latency P50 / P95 | responsive | 3.02 s / 3.02 s | 3.04 s / 3.07 s | yes |
| CPU (% of one core) | recorded | 0.36 | 0.43 | n/a |

**Reproducibility tolerance: ±1 MiB on RSS** (RSS is page-granular and the five-read medians elsewhere in this
runbook move by about ±1 MiB). Two runs of the same command differed by 0.36 MiB at P95 and by 0.4 MiB idle. That is
**n = 2**; it supports the stated tolerance, it does not prove it. Run latency reproduces to within 0.05 s.

**Baseline and what moved the number.** The comparable earlier measurement is §2, N = 10, 60 s window: 26.6 MiB
mean / 29.8 max, measured at commit `be9e8eb`. The table above was measured at `3ebf0a8` (23.5 mean / 24.1 max, N = 2).
**These are two different code versions, and the baseline was not re-measured at `3ebf0a8`.** Between them, server
code changed (`git diff be9e8eb 3ebf0a8`, 128 added lines, no deletions): `internal/auth/routes.go` (3 lines: regenerate/
edit/rewind routes), `internal/chat/handlers.go` (68: `steerRun`, `noTurnMutation`), `internal/chat/service.go` (29:
`Steer`), `internal/hermes/client.go` (28). All of it serves mid-run steer and the 501 routes, none of it is on the
send/stream path the harness drives, so I expect no effect, but **that is a reading of the diff, not a measurement**.
The lower figure here is consistent with the load difference (5x fewer concurrent runs, and the 5-minute retained-journal
window, ~6.7 KB per finished run, has not filled in a 30 s window), but this runbook does not prove the load explains it
and does not rule out a code effect. Treat the comparison as indicative only. No code optimisation was done for this
target; the target was met with no work.

**Which profile runs where.** Smoke (10 s idle, 30 s load): by hand, tabled above. CI profile (60 s idle, 300 s load):
job `bench` in `.github/workflows/ci.yml`, on pushes to `main` and manual dispatch only, never on pull requests. Full §14.2 profile (10 min + 30 min): not yet
recorded. The harness exits 0 only if runs completed, none failed, RSS was sampled and the P95 is non-zero and within the
limit; exit 1 if the target is missed or nothing was measured; exit 2 (JSON `{"error":...}`) if the scratch server never
listened.

**What this does NOT measure.**

- **Not the §14.2 duration.** 30 s of load and 10 s of idle are not the required ≥ 30 min and ≥ 10 min. The retained-journal
  growth (§2) needs about five minutes to plateau, so this row is a **floor**. The full-profile result has not been recorded;
  a 40-minute run was started and stopped before it finished. The PRD gate ("recorded benchmark evidence") is therefore
  **not yet satisfied** by this section.
- **Not the container.** The target is container working set (`memory.current`); this is process RSS of the scratch binary,
  not the release image in a cgroup, and not Hermes, the provisioner or Caddy.
- **Not the intended edge host.** It is a shared 2 vCPU guest. The CI job runs on a GitHub-hosted runner, a different
  machine; its numbers are not interchangeable with the table above and are reported only from the job's own log.
- **Not real traffic.** Fabricated upstream, 2 KB replies, no tool-progress events, no OpenUI/media, no Web Push, no
  browser, no reconnect/cancel cycles, no 1,000-turn history (the other §14.2 rows).
- **Not OOM or leak detection** beyond what a 30 s window can show.

## 7. Profiles, PWA cache privacy and long-chat scrolling (#87)

Date 2026-10-24. **Machine for every number below:** the same 2 vCPU (AMD EPYC 9754 guest), 7.6 GiB host as above,
go1.27.3, commit `1d73b9e`; Chromium 148.0.7778.96 (headless, software rendering, no GPU) driven by playwright-core;
the fake Hermes/IdP, the scratch Antares and the browser shared those 2 vCPUs. **Nothing here was measured on
production.** Production figures are external (cgroup / host RSS) and are not in this section.

### Go CPU and allocation profiles (scratch binary, `go tool pprof`)

There is no pprof HTTP endpoint (owner decision). The build-tagged scratch server (`scripts/capacity`, not the
release binary) writes profiles on a clean stop when `CAPACITY_CPUPROFILE` / `CAPACITY_MEMPROFILE` are set:

```sh
CAPACITY_CPUPROFILE=cpu.pprof CAPACITY_MEMPROFILE=allocs.pprof IDLE_SECS=60 LOAD_SECS=300 scripts/capacity/bench.sh
go tool pprof -top capacity cpu.pprof      # `capacity` = go build -tags capacity -o capacity ./scripts/capacity
go tool pprof -sample_index=alloc_space -top capacity allocs.pprof
```

The profile covers the whole process life (413 s: setup of 3 accounts, 60 s idle, 300 s load with 2 runs in flight,
200 runs, 0 failed). The benchmark itself: idle RSS 22.6 MiB, load RSS mean 27.5 / P95 29.6 / max 30.0 MiB,
0.41 % of one core, 25 FDs max (`/proc` sampling, 1 Hz): pass against 256 MiB.

| CPU (413 s wall, 1.25 s sampled = 0.3 %) | cum |
|---|---|
| `database/sql` → modernc SQLite query path | 0.64 s (51 %) |
| of which the per-request session check (`auth.signedIn` → `reserve`) | 0.57 s (46 %) |
| of which `chat.runNew` (`start`) | 0.43 s (34 %) |
| flat top: `Syscall6` 0.24 s (19 %), `futex` 3 %, SQLite parser/planner under 10 % each | |

| Allocations (`alloc_space`, 31.9 MB over the process life; pprof default sampling of 512 KiB) | flat / cum |
|---|---|
| `bufio.NewReaderSize` (one per upstream/SSE connection) | 5.7 MB flat (18 %) |
| `chat.(*Service).runNew` → `follow` → `hermes.Client.StreamEvents` | 17.6 MB cum (54 %) / 10.4 MB |
| `server.reserve` / `auth.signedIn` per-request path | 9.2 MB cum (28 %) |
| `chat.reconcile` → `hermes.GetRun` | 4.1 MB cum (13 %) |
| `chat.(*liveRun).emit` + `writeEvent` (journal and SSE frames) | 3.0 MB / 2.5 MB cum |
| `inuse_space` at exit, after a forced GC | 2.2 MB, of which 1.2 MB is the profiler itself |

**Conclusion.** Under this load Antares is idle: 0.3 % CPU and ~32 MB allocated in ~7 minutes. The cost that exists is
SQLite work on the request path (session check on every request) and per-connection buffers for the upstream
stream; nothing is retained (2.2 MB in use after GC) and nothing is a candidate for optimisation. The load is
small (2 runs in flight, 2,000-byte fake replies); with 512 KiB sampling, items under a few hundred KB are noise.
This does not replace the larger envelope in sections 2 and 4. No production profile was taken.

### Service worker: what it does, what it caches, who can reach it

Measured with `web/scripts/browser-measure.mjs` against a scratch Antares on `127.0.0.1:18080` (two approved
accounts A and B, real session cookies, the shipped `sw.js` from the embedded bundle):

```sh
CHROME=~/.cache/ms-playwright/chromium-1223/chrome-linux64/chrome PLAYWRIGHT_CORE=<path>/playwright-core \
  node web/scripts/browser-measure.mjs <state.json> 50,250
```

- **Cache Storage after install, signed in as A, and after A used the app** (chat page, `/api/me`, `/api/chats`,
  `/api/chats/{id}/messages`): one cache `antares-public-<build hash>` holding exactly 7 entries, all `GET`,
  keyed by URL path only: `/offline.html`, five `/assets/*` files and the offline stylesheet. No `Set-Cookie`, no
  account email, no message text in any body; the cache snapshot was byte-identical before and after
  signed-in use. API requests never reach the worker (`sw.js` returns before `respondWith` for `/api`), and a
  navigation is network-first with only `/offline.html` as fallback.
- **Background:** the worker has no visibility or lifecycle handler. After `Page.setWebLifecycleState`
  frozen → active the cache was unchanged. Push handlers show fixed text by `kind` and touch neither cache nor `fetch`.
- **Second account on the same browser profile** (A's cookie replaced by B's with no sign-out, the worst case): B
  sees the same cache (identical entries and hashes), which holds only public build assets, so there is nothing of
  A's to reach. `cache.match` for `/api/me`, `/api/chats`, A's messages URL and A's chat page: all miss. A's chat
  over the wire as B: 404. Browser HTTP cache: `fetch('/api/me', {cache:'only-if-cached'})` fails (not stored);
  `Cache-Control: no-store` on `/api/*` and on `/`.
- **Answer: the service-worker cache cannot leak one user's data to another**; it never holds user data.
- **One residual that is not the service worker:** composer drafts live in same-origin `localStorage`, keyed by
  the account's email (`antares:draft:v1:<email>:chat:<id>`), unencrypted. In the test A's draft was still readable by
  B's page script after the cookie swap (the app itself would not show it to B). `purgeDrafts()` runs on sign-out,
  401 and suspension while the app is open; a session that simply expires with the tab closed leaves the draft until
  the next sign-in is rejected or the user signs out. Same-origin script is the only reader, so this is a
  shared-device hygiene note, not a cross-account path through the app. Not changed here.
- **Not measured:** the offline fallback in a real browser. Playwright's `setOffline` did not reach the worker's own
  `fetch` (the page was served from the network), so that run was discarded; the fallback logic is covered only by
  `web/src/sw.test.ts`. Installed-PWA (standalone) background behaviour on a phone was not measured either.

### Long thread: windowing and scroll smoothness

Method: seed a chat through the real API (each message is a completed run against the fake Hermes), open
`/chat/<id>` in headless Chromium at 1280×800, then flick upward at 4,000 px/s, time-based, in a
`requestAnimationFrame` loop until all messages were loaded and the top reached; frame time is the rAF delta.
Control: the same flick over a plain non-virtual page of 600 fixed `<p>` rows.

| Thread | Mounted items at open / max during scroll | DOM nodes at open / end | Frame p50 / p95 / max | Frames > 50 ms | Long tasks (>50 ms) | JS heap |
|---|---|---|---|---|---|---|
| 100 messages | 50 / 100 | 381 / 608 | 66.7 / 116.7 / 183.2 ms | 43 of 52 | 0 | 5.2 MB |
| 500 messages | 50 / 100 | 433 / 264 (at top) | 66.7 / 116.7 / 216.7 ms | 215 of 280 | 1 (68 ms) | 5.9 MB |
| control, 600 plain rows | n/a | 605 | 16.7 / 16.8 / 33.4 ms | 0 of 1,174 | n/a | n/a |

Open-to-first-item: 620 ms (100 messages) and 833 ms (500 messages), page load included.

- **Windowing works:** no more than 100 items are mounted whatever the thread length, and DOM size and heap do not
  grow with it (5.2 → 5.9 MB for 5× the messages). Open cost is the first page of 50, so it does not scale with the thread either.
- **Scroll is not smooth under a fast flick on this host:** 15 fps median (66.7 ms) against 60 fps for the control.
  The figure is identical at 100 and 500 messages, so the cost is mounting and measuring new items per frame
  (Markdown render plus the layout-effect remeasure), not thread length. It is a stress flick (4,000 px/s) in
  software-rendered headless Chromium on a 2 vCPU host shared with the server; a GPU-composited phone or desktop
  will differ and this is not a field number. No fix is made here; if the jank matters, profile the mount path in
  DevTools on a target device first.
- **Not measured:** 1,000+ messages (seeding at the default 30 runs/min limit takes ~17 min per 500 messages), real
  devices, touch scrolling, streaming-reply cost while scrolled up.

## What would change these numbers

Larger or more frequent real Hermes frames, tool-progress events, OpenUI payloads, real transcripts longer than
2 KB, Web Push delivery, and browsers that reconnect. Re-run the commands above with `reply_bytes` and `hold_ms`
set to the real figures, and put the new table beside this one with its own date.

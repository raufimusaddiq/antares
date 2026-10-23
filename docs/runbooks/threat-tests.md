# Cross-user threat tests and fuzz (issue #63)

What they prove: with two approved users A and B (and an approved admin, and accounts that are not
active), B reaches none of A's data through any route this build serves. What they do **not** prove:
that Hermes keeps one profile's data from another's. That is the Phase 0 question and its verdict is
unchanged (see `docs/hermes/PHASE0-FINDINGS.md`); these tests exercise Antares' side of the boundary
against a fake Hermes.

## Commands

| Command | What it runs |
|---|---|
| `make threat` | The cross-user suite, the ownership properties and the SSE recovery properties on their fixed corpora, with `-race`. A failure reads `SURFACE BROKEN [<surface>]`. |
| `make fuzz` | The three native fuzz targets, `FUZZTIME` each (default `20s`). CI runs `make fuzz FUZZTIME=20s`. |
| `make fuzz FUZZTIME=5m` | Hunt locally. A crashing input is saved under `testdata/fuzz/<Target>/`; commit it. |

`go test -race ./...` also runs everything above on the fixed corpora, so the suite cannot be skipped
by forgetting a target.

## Where things are

- `internal/chat/threat_test.go` — table-driven cross-user suite, per surface.
- `internal/chat/fuzz_test.go` — ownership over HTTP, and SSE recovery (`FuzzOwnershipOverHTTP`, `FuzzSSERecovery`).
- `internal/store/ownership_fuzz_test.go` — ownership against a model of the store (`FuzzOwnership`).

## Surfaces

Built and probed with a foreign caller: chat read, history, send message, run status, run events, run
cancel, chat list/create. Each must answer `404` with a body identical to the answer for an id that
never existed, with no marker of A's data, no change to A's rows, and no Hermes call.

Not built (listed in `notBuilt` in `threat_test.go`, each with its reason): search index, saved drafts,
templates, push subscriptions, exports, voice uploads, preview sandbox, rewind/fork, steer, delayed
notifications. The suite asserts they stay unrouted (or a `501` stub) and fails when one starts to
answer, so building a surface forces writing its cross-user test.
`TestEveryChatRouteIsClassified` does the same for any route added to the handler table.

## Adding a surface

1. Add its route to `builtProbes` (id in the path) or `idFree`.
2. Remove it from `notBuilt`.
3. If it stores user data (drafts, push, exports…), add its seed to `newVictim` and its marker to
   `victim.markers`, so a leak is detected by content, not only by status code.

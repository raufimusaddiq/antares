# Runbook — private memory scoping (slice 5, PRD §5.4, §5.6)

## What Antares guarantees, and how it is tested

| Rule | Mechanism | Test |
|---|---|---|
| `MEM-001` one profile per user | `tenant_bindings.user_id` and `profile_name` are `UNIQUE`; chats reference `(binding, user)` by composite FK | `internal/store` tests, `TestPhase1Gate/3` |
| `MEM-002` server-derived `X-Hermes-Session-Key`, per user not per chat | `chatui:user:<user id>`, set in `internal/chat/service.go`; any client-supplied key/header/field is refused (400) before it can be forwarded | `TestSessionKeyIsPerUserAndSessionIsPerChat`, `TestClientSuppliedProfileKeyAndSessionNeverReachHermes`, `TestMemoryOfOneUserIsNeverRecalledForAnother` |
| `MEM-003` one Hermes session per chat, opaque ids, never across users | `chats.hermes_session_id` unique per binding; ids are app-generated 32-hex | same, plus `TestHistoryIsOwnerScopedIncludingCursors` |
| `MEM-006` block chat on misrouting | every call re-reads the binding in the database (default deny); a rejected key fails closed with no other credential tried | `TestGateDeniesBeforeAnyHermesRequest`, `TestWrongProfileKeyFailsClosedWithoutFallback` |
| §5.6 simultaneous A/B streams never cross | one journal per run, owner-checked subscription | `TestSimultaneousStreamsNeverCross` |
| §5.6 B cannot reach A's chats/runs/history/cursors by id | ownership in every query; 404 not 403 | `TestTwoUsersChatPrivately`, `TestHistoryIsOwnerScopedIncludingCursors`, `TestPhase1Gate/3` |
| §5.6 removing the mapping stops all calls | `store.ActiveBinding` joins approval + tenant + binding each time | `TestGateDeniesBeforeAnyHermesRequest` |

The recall tests use a fake Hermes whose memory is **keyed by the profile the request arrives on**. They prove Antares
routes each user to their own profile, key, session and session key — the part Antares controls. They do not, and
cannot, prove that real Hermes keeps one profile's memory from another's.

## What is NOT verified: memory isolation in the live container

**Status: UNVERIFIED. Do not describe this product as safe for mutually untrusted users.**

- Reaching it needs two provisioned test profiles on the live Hermes and writes to their memory. The ticket
  (#24) does not authorise profile create/delete or writes in the production container, and `AGENTS.md` /
  `docs/runbooks/deployment.md` limit those to what a ticket names. None was done.
- `docs/hermes/PHASE0-FINDINGS.md` records that `/p/<profile>/…` returned 404 for a non-default profile
  (`gateway.profile_routes` unset), so even with permission the route may not resolve until the owner registers it.
- The Phase 0 **FAIL** stands, unchanged, together with its second blocker (no tool filesystem isolation). Nothing in this
  slice weakens or works around either.

The test that closes the memory item is written and waiting: `internal/hermes/live_memory_test.go` (build tag
`livehermes`; compiled and vetted, **never executed**). To run it, the owner provisions two disposable profiles, registers
their routes, and runs:

```
ANTARES_LIVE_HERMES_URL=http://hermes:8642 \
ANTARES_LIVE_PROFILE_A=testa ANTARES_LIVE_KEY_A=… ANTARES_LIVE_PROFILE_B=testb ANTARES_LIVE_KEY_B=… \
  go test -tags livehermes -run TestLive -v -count=1 ./internal/hermes
```

It checks that A recalls its own token in a fresh session (otherwise the result is "inconclusive", not a pass), that B does
not, that each key is refused on the other's route, and that B cannot read or run in A's session. It does **not** test
shell/file tools reading sibling profile directories — that is the Phase 0 blocker and needs its own test once tools are
enabled for tenants.

# Acceptance scenarios → runnable tests

Issue #154 (and the 2026-10-26 audit's adjacent cases). Each row names the test that runs the scenario end to end and
asserts its outcome. Feature-parity tracking per requirement lives in `docs/FEATURE_PARITY_AIXELL.md` (#112).

| Scenario | Test | What it asserts |
|---|---|---|
| UXP-ACC-003 suspension while a reply is finishing, then reattach | `internal/chat/acceptance_test.go` `TestAcceptanceSuspendedWhileFinishingThenReattach` | stream ends `run.disconnected`, never `run.completed`; run finishes upstream and is stored once; after reinstatement the reattached stream shows exactly one `run.completed`; one Hermes run |
| UXP-ACC-004 1000-turn transcript with compacted turns: search and export paging | `TestAcceptanceThousandTurnCompactedSearchAndPaging` | search finds turn 3 by a rare word; paging returns all 1000 turns once each, `total` 1000 on every page |
| UXP-ACC-014/016 ten disconnect/reconnect cycles with tools and OpenUI | `TestAcceptanceTenReconnectCyclesWithToolsAndOpenUI` | assembled answer exact (including the OpenUI fence), no event id twice, 10 tool starts, final `run.completed`, one Hermes run |
| Approval survives a reload | `internal/chat/prompts_test.go` `TestRunNeedingApprovalDoesNotProceedUntilApproved` | stream and in-memory journal dropped; the pending approval is read back from the database and still answerable |
| Tool failure, missing tool id, duplicate id | `web/src/Activity.test.tsx` | failed rows say failed; unmatched starts/ends are untracked, never done |
| `run.resumed` clears tools as well as text | `web/src/Activity.test.tsx` ("run.resumed clears tools…") | no stale tool rows after a replay |
| Bounded tool history for a noisy stream | `web/src/Activity.test.tsx` ("a noisy stream stays bounded…") | 500 calls → at most 40 rows, counts exact |
| Reconnect without duplicates (base case) | `internal/chat/chat_test.go` `TestReconnectResumesWithoutDuplicating` | resume by `Last-Event-ID`, no event twice |

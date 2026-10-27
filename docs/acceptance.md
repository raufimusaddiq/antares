# Acceptance scenarios → runnable tests

Issue #154 (and the 2026-10-26 audit's adjacent cases). Each row names the test that runs the scenario end to end and
asserts its outcome. Per-requirement parity tracking is issue #112.

| Scenario | Test | What it asserts |
|---|---|---|
| UXP-ACC-003 suspension while a reply is finishing, then reattach | `internal/chat/acceptance_test.go` `TestAcceptanceSuspendedWhileFinishingThenReattach` | stream ends `run.disconnected`, never `run.completed`; while suspended, reattaching to the run, reading it and listing chats are all refused (`403 ACCOUNT_SUSPENDED`); run finishes upstream and is stored once; after reinstatement the reattached stream shows exactly one `run.completed`; one Hermes run |
| UXP-ACC-004 1000-turn transcript with compacted turns: search and export | `TestAcceptanceThousandTurnCompactedSearchAndPaging`; `web/src/ExportChat.test.tsx` ("assembles 1000 turns in order across pages") | 600 compacted turns and interleaved tool messages; search finds a compacted turn and ignores tool output; paging returns exactly the 1000 turns once each in chronological order; the Markdown export of a paged 1000-turn history lists every turn once, in order. PDF export is the browser's print of the same page (`ExportChat.test.tsx`) |
| UXP-ACC-014/016 ten disconnect/reconnect cycles with tools and OpenUI | `TestAcceptanceTenReconnectCyclesWithToolsAndOpenUI` | assembled answer exact (including the OpenUI fence), no event id twice, 10 tool starts, final `run.completed`, one Hermes run |
| Approval survives a reload | `internal/chat/prompts_test.go` `TestRunNeedingApprovalDoesNotProceedUntilApproved` | stream and in-memory journal dropped; the pending approval is read back from the database and still answerable |
| Tool failure, missing tool id, duplicate id | `web/src/Activity.test.tsx` | failed rows say failed; unmatched starts/ends are untracked, never done |
| `run.resumed` clears tools as well as text | `web/src/Activity.test.tsx` ("run.resumed clears tools…") | no stale tool rows after a replay |
| Bounded tool history for a noisy stream | `web/src/Activity.test.tsx` ("a noisy stream stays bounded…") | 500 calls → at most 40 rows, counts exact |
| UXP-ACC-014/016 bounded: reconnect after the replay buffer overflowed | `TestAcceptanceReconnectPastTheReplayBound` | `run.resumed {replace:true}` first; replayed deltas fewer than pushed and within `journalBytes` (4 MiB); ends on the stored `run.completed` with the full output; one Hermes run |
| Reconnect without duplicates (base case) | `internal/chat/chat_test.go` `TestReconnectResumesWithoutDuplicating` | resume by `Last-Event-ID`, no event twice |

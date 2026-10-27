# Feature parity with the Aixell reference — tracking table (PRD §29.3, issue #112)

Version 1, 2026-10-28. Verified against Hermes 0.22.5.

Hermes observations are from the running container, `nousresearch/hermes-agent` v0.22.5 (image `sha256:9c5561579f3f…`),
read-only. The gateway's full route table, version and `/health` body are committed as
`docs/hermes/fixtures/hermes-route-probe.json` (made by `scripts/hermes-route-probe.sh`); code-level observations cite
`gateway/platforms/api_server*.py` and `cron/*.py` by file:line. The earlier Phase 0 probe (v0.22.2,
`docs/hermes/PHASE0-FINDINGS.md`) is older than the running version.

**Re-verify after every Hermes upgrade:** run `sudo scripts/hermes-route-probe.sh > docs/hermes/fixtures/hermes-route-probe.json`,
then update the "Verified against Hermes" line above. `internal/parity` fails while the two versions differ, and fails if
a route family the blocked rows say is absent (memory, notes, facts, speech, quota) appears in the probe.

One row per requirement in PRD §21 (UXP-*), §26.1 (each domain row), §27 (LLM-*, CTX-*, MEDIA-EXT-*, QUEUE-*), §28
(KNOW-*, IMPORT-*) and §29 (LLM-ACC-*). `internal/parity/parity_test.go` fails when a row is missing or repeated, when a
status is not one of the four below, when a TESTED or IMPLEMENTED row names no test that exists in this repository, or
when an UPSTREAM_BLOCKED row names no probed endpoint or does not cite the route probe.

**What the test does not prove:** it checks that cited tests and fixtures exist, not that a test exercises the row's
behaviour; that rests on review of each row. Read the table as a traceable index, not as a coverage proof.

| Status | Meaning |
|---|---|
| `TESTED` | Built, and the named automated tests prove it. Real-device or live-provider checks still owed are said in the row. |
| `IMPLEMENTED` | Built and covered by the named tests, but part of the requirement is only verifiable live (said in the row). |
| `UPSTREAM_BLOCKED` | The deployed Hermes has no route for it; the row names what was probed and found absent. |
| `OWNER_ACCEPTED_EXCEPTION` | A deliberate divergence or an owner decision, recorded with its reason. |

Deliberate divergences from Aixell, never "forgotten features": one logical assistant (no profile picker), no shared
memory between users, no direct reads of Hermes databases or profile files, no Antares-side provider calls.

## §21 — mature chat UX (UXP)

| ID | Requirement | Pri | Source module | Owner | Antares contract | Hermes v0.22.5 | Status | Evidence |
|---|---|---|---|---|---|---|---|---|
| UXP-001 | Installable PWA | P1 | `web/public/sw.js`, `web/src/pwa.ts`, `web/public/manifest.webmanifest` | Antares | manifest, icons, service worker, update prompt (`web/public`, `sw.ts`) | n/a | TESTED | `TestPWAPublicAssets`, `TestServiceWorkerIsServedFreshAndIsNotTheShell`, `web/src/pwa.test.tsx`, `web/src/sw.test.ts`. Install on a real phone: #174. |
| UXP-002 | Push on agent completion | P1 | `internal/push`, `web/src/Notifications.tsx` | Antares | `/api/push/*`, `/api/me/notifications`; generic text only | n/a | TESTED | `TestPushRoutesNeedAnEligibleAccount`, `TestUnconfiguredPushIsReportedNotFaked`, `web/src/push.test.ts`, `web/src/Notifications.test.tsx` |
| UXP-003 | Full-text search of own chats | P1 | `internal/chat/search.go`, `web/src/Search.tsx` | Antares | `GET /api/search/chats`, `GET /api/chats/{id}/search` | n/a (app index of own history) | TESTED | `TestSearchFindsTitlesAsTitleHitsWithNoTurn`, `TestSearchHighlightsMatchedSpanAndNeverEmitsMarkup`, `TestAcceptanceThousandTurnCompactedSearchAndPaging`, `web/src/Search.test.tsx` |
| UXP-004 | Voice input / dictation | P1 | `web/src/Recorder.tsx`, `web/src/Dictation.tsx` | Antares | recorder (device-only) + dictation via the browser's own speech service with disclosure (#167) | no STT route | IMPLEMENTED | `web/src/Recorder.test.tsx`, `web/src/Dictation.test.tsx`. Real speech recognition needs a real device: #174. |
| UXP-005 | Markdown composer | P1 | `web/src/composerEdit.ts`, `web/src/Chat.tsx` (Composer) | Antares | multiline editor, shortcuts, Tab in code | n/a | TESTED | `web/src/composerEdit.test.ts`, `web/src/ComposerKeys.test.tsx`, `web/src/Keyboard.test.tsx` |
| UXP-006 | Slash prompt templates | P1 | `internal/chat/drafts.go`, `web/src/Templates.tsx` | Antares | `/api/me/templates` CRUD, autocomplete | n/a | TESTED | `TestTemplatesAreOwnerScopedLiteralText`, `TestTemplateValidationAndBounds`, `web/src/Templates.test.tsx` |
| UXP-007 | Draft per conversation | P1 | `internal/chat/drafts.go`, `web/src/drafts.ts`, `web/src/DraftSync.tsx` | Antares | device copy + `/api/chats/{id}/draft` (revisioned) | n/a | TESTED | `TestDraftIsOwnerScoped`, `TestConcurrentDraftWritesResolveByRevision`, `web/src/drafts.test.tsx`, `web/src/DraftSync.test.tsx` |
| UXP-008 | Context usage meter | P1 | `internal/chat/usage.go` | Antares | `GET /api/chats/{id}/usage`; unknown never zero | run `usage` frames | TESTED | `TestUsageOccupancyIsExplicitlyUnknownNeverZero`, `TestCancelledRunUsageIsCounted`, `web/src/Chat.test.tsx` |
| UXP-009 | Cost / quota / budget | P1 | `internal/chat/spending.go`, `internal/chat/usage_range.go` | Antares | token budget, spending limits (#152), usage dashboard (#151) | no balance route | TESTED | `TestDailyHardSpendingLimitIsAnAdmissionGate`, `TestAdmissionGateIsAtomicWithTheInsert`, `TestUsageRangeIsOwnerScopedAndHonest`, `TestBudgetIsPerUserAcrossChats` |
| UXP-010 | Turn editing / regenerate / rewind | P1 | `internal/chat/handlers.go`, `web/src/Chat.tsx` (turn controls) | Antares | regenerate and edit write a new turn; destructive rewind refused (no native Hermes route) | session fork only | TESTED | `TestSteerAndTurnControlsRefuseAnotherUsersChat`, `TestRewindDoesNotOrphanSSEStreams`, `web/src/TurnControls.test.tsx`, `web/src/TurnReuse.test.tsx` |
| UXP-011 | Steer while running | P1 | `internal/chat/handlers.go` (steer), `web/src/Chat.tsx` (steer) | Antares | `POST /api/runs/{id}/steer` | `POST /v1/runs/{id}/steer` | TESTED | `TestSteerLandsOnOwnRunningRun`, `TestSteerRefusedWhenRunIsOverOrHermesDeclines`, `web/src/Steer.test.tsx` |
| UXP-012 | Resilient reconnect | P0 | `internal/chat/service.go`, `internal/chat/handlers.go` (SSE journal) | Antares | SSE journal with Last-Event-ID, restart recovery | `GET /v1/runs/{id}` reconcile | TESTED | `TestReconnectResumesWithoutDuplicating`, `TestRestartThenReconnectReattaches`, `TestAcceptanceReconnectPastTheReplayBound` |
| UXP-013 | HTML/SVG artifact preview | P1 | `internal/chat/artifacts.go`, `web/src/Artifacts.tsx` | Antares | sandboxed preview origin, attachment downloads | n/a | TESTED | `TestArtifactPreviewIsSandboxedAndDownloadIsAnAttachment`, `TestArtifactRefusals`, `web/src/Artifacts.test.tsx` |
| UXP-014 | Export Markdown / PDF | P1 | `web/src/exportChat.ts`, `web/src/ExportChat.tsx` | Antares | browser-side export of own paged history | n/a | TESTED | `web/src/ExportChat.test.tsx` (1000 turns through both buttons), `web/src/printCss.test.ts` |
| UXP-015 | Day-to-day ergonomics | P1 | `web/src/ChatList.tsx`, `web/src/VirtualList.tsx` | Antares | rail, pin/rename/archive, scroll-to-bottom, keyboard | n/a | TESTED | `TestRenameUpdatesListAndSearch`, `web/src/ChatManage.test.tsx`, `web/src/ScrollBehavior.test.tsx`, `web/src/MobileShell.test.tsx`. Fast-fling smoothness on devices: #170. |
| UXP-016 | PWA share target | P2 | `web/public/sw.js`, `web/src/pwa.ts` (share target), `web/src/drafts.ts` | Antares | shared text lands in a draft, never sent | n/a | TESTED | `web/src/pwa.test.tsx`, `web/src/DraftSend.test.tsx`. OS share sheet on a phone: #174. |

## §26.1 — capability domains

| ID | Requirement | Pri | Source module | Owner | Antares contract | Hermes v0.22.5 | Status | Evidence |
|---|---|---|---|---|---|---|---|---|
| §26/Authentication | Login/logout, all devices, safety | P0 | `internal/auth` | Antares | Google OIDC, approval, sign out everywhere, admin revoke | n/a | TESTED | `TestAdminRevokeSessionsSignsTheUserOutEverywhere`, `TestAuditRecordsLoginLogoutAndRejection`, `web/src/SignOutEverywhere.test.tsx` |
| §26/Agent selection | One logical assistant | P0 | `internal/auth/routes.go`, `internal/chat/service.go` (gate) | Owner | the user's own server-bound profile; no picker | `/p/{profile}` ingress | OWNER_ACCEPTED_EXCEPTION | Divergence by design (PRD §3). `TestClientSuppliedProfileKeyAndSessionNeverReachHermes`, `TestCallerSuppliedProfileKeyOrSessionIsRejected` prove no caller-chosen profile reaches Hermes. |
| §26/Session/history | Own chats, pin/archive/rename/delete, history | P0 | `internal/chat/manage.go`, `internal/chat/history.go` | Antares | `/api/chats*` | `/api/sessions/*` | TESTED | `TestCrossUserThreatBuiltSurfaces`, `TestAcceptanceThousandTurnCompactedSearchAndPaging`, `web/src/ChatManage.test.tsx` |
| §26/Streaming | Markdown, tool progress, cancel, approval, reconnect | P0 | `internal/chat/service.go`, `internal/chat/handlers.go` | Antares | SSE per run | `/v1/runs/{id}/events` | TESTED | `TestStreamingRunCompletesInOrderWithToolEvents`, `TestRunNeedingApprovalDoesNotProceedUntilApproved`, `web/src/Activity.test.tsx` |
| §26/Model selection | Discover model options, per chat | P1 | `internal/chat/models.go`, `web/src/ModelPicker.tsx` | Antares | `/api/models`, `/api/chats/{id}/model` | `/api/model/options` | TESTED | `TestModelChoiceIsPerChatValidatedAndSent`, `TestModelsAreTheKenariCatalogue`, `web/src/ModelPicker.test.tsx` |
| §26/Advanced model settings | Reasoning effort, tier, hide models | P1 | `internal/chat/modelprefs.go` | Antares | chat settings, model visibility | `model_options` | TESTED | `TestModelSettingsAreScopedToTheOwner`, `TestModelVisibilityIsPerUserAndPolicyWins`, `web/src/ModelGate.test.tsx` |
| §26/Rich model metadata | Modalities, context, price | P1 | `internal/kenari`, `internal/chat/models.go` | Antares | catalogue metadata with its age | `/api/model/options` | TESTED | `TestModelsSayHowOldTheCatalogueIs`, `TestModelsNeverCarryTheSharedAccountQuota`, `web/src/ModelPicker.test.tsx` |
| §26/Context/usage | Context bar, per turn/session/user usage | P1 | `internal/chat/usage.go`, `internal/chat/usage_range.go` | Antares | chat usage, usage dashboard | run `usage` frames | TESTED | `TestUsageOccupancyIsExplicitlyUnknownNeverZero`, `TestUsageRangeIsOwnerScopedAndHonest`, `web/src/UsageDashboard.test.tsx` |
| §26/Provider account usage | Quota/balance only if available | P2 | `internal/kenari/account.go`, `internal/auth/admin_usage.go` | Antares | admin-only Kenari usage | not in Hermes | TESTED | `TestKenariUsageIsAdminOnlyAndNeverCarriesTheKey`, `TestKenariUsageOutageIsUnavailableNotZero`, `web/src/ProviderUsage.test.tsx` |
| §26/Budgets | Soft and hard limits, graphs | P1 | `internal/chat/spending.go`, `internal/store/usage.go` | Antares | `/api/me/budget`, `/api/me/spending-limits`, `/api/me/usage` | n/a | TESTED | `TestDailyHardSpendingLimitIsAnAdmissionGate`, `TestHardLimitHoldsWhenTheChatsRunFinishesDuringTheSend`, `TestUsageRangeIsOwnerScopedAndHonest` |
| §26/Multimodal composer | Images: picker, paste, drop | P1 | `internal/chat/media.go`, `web/src/ImageAttachments.tsx` | Antares | owner-staged images sent inline | `image_url` input parts | TESTED | `TestImageSendUsesCheckedModelAndInlineBytes`, `TestImageCrossOwnerDenied`, `web/src/ImageAttachments.test.tsx`. Live model reception: #101. |
| §26/Generative media | Owned gallery for generated media | P1 | — (`docs/runbooks/media.md`) | Hermes upstream | — | `/v1/runs` returns raw `MEDIA:` paths; `/v1/artifacts/download/{id}` is one-shot and browser-control only | UPSTREAM_BLOCKED | Probed `/v1/runs` (no inlining in `api_server_runs.py`) and `/v1/artifacts/download/{artifact_id}`; contract proposal in #168. Route table: `docs/hermes/fixtures/hermes-route-probe.json`. |
| §26/Voice | Record and dictate | P1 | `web/src/Dictation.tsx`, `docs/runbooks/voice.md` | Antares | recorder + browser dictation (#167) | no STT route in `/v1/*` | IMPLEMENTED | `web/src/Recorder.test.tsx`, `web/src/Dictation.test.tsx`. STT language choice is the browser's (`navigator.language`). |
| §26/Interactive responses | OpenUI with per-chat Visualize | P1 | `web/src/openui/*`, `internal/chat/personality.go` | Antares | safe renderer + toggle (#153) | `POST /v1/runs` `instructions` | TESTED | `TestVisualizeIsPerChatAndReachesOnlyItsRuns`, `web/src/openui/openui.test.tsx`, `web/src/VisualizeToggle.test.tsx` |
| §26/Composer | Markdown, templates, drafts | P1 | `web/src/Chat.tsx` (Composer), `web/src/Templates.tsx` | Antares | as UXP-005..007 | n/a | TESTED | `web/src/composerEdit.test.ts`, `web/src/Templates.test.tsx`, `TestDraftIsOwnerScoped` |
| §26/Message queue | Queue next messages | P1 | `internal/chat/queue.go`, `web/src/QueuePanel.tsx` | Antares | `/api/chats/{id}/queue*` | n/a (normal runs) | TESTED | `TestQueuedMessageIsSentOnceAfterTheRunCompletes`, `TestQueueIsOwnerScoped`, `TestCancelPausesTheQueue`, `web/src/Queue.test.tsx` |
| §26/Turn controls | Regenerate, edit, steer | P1 | `internal/chat/handlers.go` | Antares | as UXP-010/011 | `/v1/runs/{id}/steer` | TESTED | `TestSteerLandsOnOwnRunningRun`, `web/src/TurnControls.test.tsx` |
| §26/Memory/knowledge | Facts, notes, skills | P1 | `web/src/MemorySkills.tsx`, `docs/runbooks/memory-skills.md` | Hermes upstream | skills read-only; facts: no route | no memory route; `GET /v1/skills` only | UPSTREAM_BLOCKED | Probed the `/v1/*` and `/api/*` route table: no memory or notes route (`/v1/skills` lists skills only). Skills list: `web/src/MemorySkills.test.tsx`; no memory path routed: `TestCrossUserThreatNotYetBuilt`. Route table: `docs/hermes/fixtures/hermes-route-probe.json`. |
| §26/Persona | Per-user instructions | P1 | `internal/chat/personality.go` | Antares | `/api/me/personality` → run `instructions` | `POST /v1/runs` `instructions` | TESTED | `TestPersonalityAppliesToTheOwnersNextRunOnly`, `TestPersonalityInputIsStrict`, `web/src/MemorySkills.test.tsx` |
| §26/External chat | Read-only import | P2 | — (#116) | Hermes upstream | — | `POST /api/sessions` creates an empty session only | UPSTREAM_BLOCKED | Probed `/api/sessions` (`_handle_create_session`) and `/v1/runs` `conversation_history` (per turn, not persisted); no per-user source exists. #116. Route table: `docs/hermes/fixtures/hermes-route-probe.json`. |
| §26/PWA/mobile | Install, push, share target, viewport | P1 | `web/public/sw.js`, `web/src/pwa.ts`, `web/src/styles.css` | Antares | as UXP-001/002/016 | n/a | TESTED | `TestPWAPublicAssets`, `web/src/MobileShell.test.tsx`, `web/src/pwa.test.tsx`. Physical-device run: #174. |
| §26/Appearance | Theme, font size, focus, sidebar | P1 | `web/src/appearance.ts`, `web/src/Settings.tsx` | Antares | per-device preferences (#113) | n/a | TESTED | `web/src/Settings.test.tsx`, `web/src/Keyboard.test.tsx` |
| §26/Artifacts/export | Sandbox preview, MD/PDF | P1 | `internal/chat/artifacts.go`, `web/src/exportChat.ts` | Antares | as UXP-013/014 | n/a | TESTED | `TestArtifactPreviewIsSandboxedAndDownloadIsAnAttachment`, `web/src/ExportChat.test.tsx` |
| §26/Diagnostics | Version, update prompt, connection | P1 | `internal/server/server.go` (version), `web/src/Settings.tsx` | Antares | `GET /api/version`, Diagnostics panel (#113) | n/a | TESTED | `TestVersionIsTheRevisionOnly`, `web/src/Settings.test.tsx`, `web/src/swRotation.test.ts` |
| §26/Scheduled/agent operations | User-scoped jobs, tools | P1 | `internal/chat/jobs.go`, `internal/hermes/jobs.go`, `web/src/Jobs.tsx` | Antares | `/api/jobs*` behind `ANTARES_JOBS` (#105) | `/api/jobs*` per profile | IMPLEMENTED | `TestJobsAreOwnerScoped`, `TestSuspensionPausesTheUsersJobsUpstream`, `TestJobsAreOffUnlessSwitchedOn`, `web/src/Jobs.test.tsx`. Off until the live isolation probe is recorded. |

## §27 — LLM, context and media contract

| ID | Requirement | Pri | Source module | Owner | Antares contract | Hermes v0.22.5 | Status | Evidence |
|---|---|---|---|---|---|---|---|---|
| LLM-001 | Discover deployed model options | P1 | `internal/chat/models.go`, `internal/hermes/discovery.go` | Antares | `/api/models` from the deployed catalogue | `/api/model/options`, `/v1/capabilities` | TESTED | `TestModelsNeedTheAdvertisedRoute`, `TestDiscoverDerivesFromTheRealCapturedPayloads`, `TestModelsAreTheKenariCatalogue` |
| LLM-002 | Per-chat selection | P1 | `internal/chat/modelprefs.go` | Antares | `PUT /api/chats/{id}/model` | run `model`/`provider` | TESTED | `TestModelChoiceIsPerChatValidatedAndSent`, `TestModelSettingsAreScopedToTheOwner` |
| LLM-003 | Reasoning-effort chooser | P1 | `internal/chat/modelprefs.go` | Antares | validated per model | `model_options.reasoning` | TESTED | `TestModelChoiceIsPerChatValidatedAndSent`, `web/src/ModelPicker.test.tsx` |
| LLM-004 | Service tier | P1 | `internal/chat/modelprefs.go` | Antares | shown unsupported unless the catalogue says so | `model_options.service_tier` | TESTED | `TestModelSettingsAreScopedToTheOwner`, `web/src/ModelPicker.test.tsx` |
| LLM-005 | Hide/show models per user, admin policy | P1 | `internal/chat/models.go`, `internal/auth/admin.go` | Antares | `/api/me/model-visibility`, admin policy | n/a | TESTED | `TestModelVisibilityIsPerUserAndPolicyWins`, `TestModelPolicyIsAdminOnlyAndAudited`, `TestBlockedDefaultModelIsNotExempt` |
| LLM-006 | Model badges and detail | P1 | `internal/chat/models.go`, `web/src/ModelPicker.tsx` | Antares | catalogue modalities, context, age | `/api/model/options` | TESTED | `TestModelsSayHowOldTheCatalogueIs`, `web/src/ModelPicker.test.tsx` |
| LLM-007 | Searchable, keyboard-accessible picker | P1 | `web/src/ModelPicker.tsx` | Antares | refresh on open, unavailable model shown | n/a | TESTED | `web/src/ModelPicker.test.tsx`, `web/src/ModelGate.test.tsx` |
| LLM-008 | Per-chat Visualize/OpenUI toggle | P1 | `internal/chat/manage.go` (visualize), `web/src/VisualizeToggle.tsx` | Antares | `PATCH /api/chats/{id}` `visualize` (#153) | `POST /v1/runs` `instructions` → ephemeral system prompt | TESTED | `TestVisualizeIsPerChatAndReachesOnlyItsRuns`, `web/src/VisualizeToggle.test.tsx`, `web/src/openui/openui.test.tsx` |
| CTX-001 | Context bar = last prompt / verified window | P1 | `internal/chat/usage.go` | Antares | unknown when either side is unknown | run `usage` | TESTED | `TestUsageOccupancyIsExplicitlyUnknownNeverZero`, `web/src/Chat.test.tsx` |
| CTX-002 | Compaction indicator, all turns kept | P1 | `internal/chat/history.go`, `web/src/history.ts` | Antares | paged history across compaction | `/api/sessions/{id}/messages` | TESTED | `TestAcceptanceThousandTurnCompactedSearchAndPaging`, `TestArtifactStaleSeqIsRefusedAfterCompaction`, `web/src/history.test.ts` |
| CTX-003 | Token categories, by model, windows | P1 | `internal/chat/usage_range.go` | Antares | chat usage + `/api/me/usage` | run `usage` | TESTED | `TestUsageRangeIsOwnerScopedAndHonest`, `TestCancelledRunUsageIsCounted`, `web/src/UsageDashboard.test.tsx` |
| CTX-004 | Provider spend/quota windows, read-only | P2 | `internal/kenari/account.go` | Antares | admin Kenari usage; never in user model data | not in Hermes | TESTED | `TestKenariUsageOutageIsUnavailableNotZero`, `TestModelsNeverCarryTheSharedAccountQuota` |
| CTX-005 | Warnings vs hard limit distinct | P1 | `internal/chat/spending.go` | Antares | soft warnings never block; hard limit is an admission gate | n/a | TESTED | `TestSoftSpendingLimitsWarnAndNeverBlock`, `TestDailyHardSpendingLimitIsAnAdmissionGate` |
| MEDIA-EXT-001 | Photos, paste, drop, picker, camera | P1 | `internal/chat/media.go`, `web/src/ImageAttachments.tsx` | Antares | staged owner-checked images | `image_url` parts | TESTED | `TestImageStagingOwnerOnlyAndValidated`, `TestImageUploadRefusals`, `web/src/ImageAttachments.test.tsx` |
| MEDIA-EXT-002 | Vision only for models that take images | P1 | `internal/chat/media.go` | Antares | catalogue-checked model at send | n/a | TESTED | `TestImageSendRefusedWhenVisionIsNotConfirmed`, `TestImageSendChecksTheChatsChosenModelNotTheDefault` |
| MEDIA-EXT-003 | Generated images: owned, lightbox, download | P1 | — (#168) | Hermes upstream | — | `/v1/runs` raw `MEDIA:` paths | UPSTREAM_BLOCKED | Probed `/v1/runs` and `/v1/artifacts/download/{artifact_id}` (one-shot, browser-control only). #168. Route table: `docs/hermes/fixtures/hermes-route-probe.json`. |
| MEDIA-EXT-004 | Generated video as a job | P2 | — (#168) | Hermes upstream | — | no media job route in `/v1/*` | UPSTREAM_BLOCKED | Probed the `/v1/*` route table: no video job or progress route. #168. Route table: `docs/hermes/fixtures/hermes-route-probe.json`. |
| MEDIA-EXT-005 | Audio/TTS player; editable dictation | P2 | `web/src/Dictation.tsx`; TTS — (#168) | Hermes upstream | dictation editable (#167); no TTS output | no TTS route in `/v1/*` | UPSTREAM_BLOCKED | Probed the `/v1/*` route table: no audio output route. Dictation part: `web/src/Dictation.test.tsx`. Route table: `docs/hermes/fixtures/hermes-route-probe.json`. |
| MEDIA-EXT-006 | Tool activity, citations | P1 | `internal/chat/live.go`, `web/src/runState.ts` | Antares | activity stream with stage and outcome | run tool events | TESTED | `TestStreamingRunCompletesInOrderWithToolEvents`, `TestActivityEventsCarryStageAndToolOutcomeButNoReasoning`, `web/src/Activity.test.tsx` |
| MEDIA-EXT-007 | Lazy rich components | P1 | `web/src/openui/Block.tsx`, `web/vite.config.ts` | Antares | OpenUI and charts in on-demand chunks | n/a | TESTED | `TestOpenUIChunksAreNotInTheInitialBundle`, `TestServiceWorkerPrecachesShellNotOptionalChunks` |
| QUEUE-001 | Queue next messages | P1 | `internal/chat/queue.go`, `internal/store/queue.go` | Antares | `/api/chats/{id}/queue*` | normal runs | TESTED | `TestQueuedMessageIsSentOnceAfterTheRunCompletes`, `web/src/Queue.test.tsx` |
| QUEUE-002 | Scoped by user + chat, persisted | P1 | `internal/store/queue.go` | Antares | SQLite `queue_items` | n/a | TESTED | `TestQueueIsOwnerScoped`, `TestCrossUserThreatBuiltSurfaces` |
| QUEUE-003 | Exactly-once dispatch | P1 | `internal/store/queue.go` | Antares | claim with compare-and-swap + idempotency key | `Idempotency-Key` | TESTED | `TestQueuedMessageIsSentOnceAfterTheRunCompletes`, `TestQueueIsNotStrandedWhenTheRunEndsDuringARefusedClaim` |
| QUEUE-004 | Steer is not queue; pause on stop/failure | P1 | `internal/chat/queue.go`, `internal/chat/handlers.go` (steer) | Antares | queue pauses on cancel, failure or unknown | n/a | TESTED | `TestCancelPausesTheQueue`, `TestPauseQueueIfWaiting`, `web/src/Steer.test.tsx` |

## §28 — memory, knowledge, personality, external history

| ID | Requirement | Pri | Source module | Owner | Antares contract | Hermes v0.22.5 | Status | Evidence |
|---|---|---|---|---|---|---|---|---|
| KNOW-001 | One Memory & Skills panel | P1 | `web/src/MemorySkills.tsx` | Antares | panel with memory status, skills, instructions (#111) | `GET /v1/skills`; no memory route | IMPLEMENTED | `web/src/MemorySkills.test.tsx`, `TestPersonalityAppliesToTheOwnersNextRunOnly`. Facts part: KNOW-002. |
| KNOW-002 | Read-only memory facts | P1 | — (`docs/runbooks/memory-skills.md`) | Hermes upstream | — | no memory route in `/v1/*` or `/api/*` | UPSTREAM_BLOCKED | Probed the gateway route table (`_http_route_table`, every `/v1/*` and `/api/*` path): no facts route. `TestCrossUserThreatNotYetBuilt` pins that none is routed. Route table: `docs/hermes/fixtures/hermes-route-probe.json`. |
| KNOW-003 | MEMORY.md / USER.md notes | P1 | — (`docs/runbooks/memory-skills.md`) | Hermes upstream | — | no notes route in `/v1/*` or `/api/*` | UPSTREAM_BLOCKED | Probed the gateway route table (every `/v1/*` and `/api/*` path): no notes route; Antares never mounts or reads profile files. Route table: `docs/hermes/fixtures/hermes-route-probe.json`. |
| KNOW-004 | List/search skills, read-only | P1 | `internal/hermes/discovery.go`, `web/src/MemorySkills.tsx` | Antares | capability inventory skills, searchable | `GET /v1/skills` (name, description, category) | TESTED | `TestCapabilitiesReportsWhatHermesConfirmsAndNoMore`, `web/src/MemorySkills.test.tsx`. Skill text: not served by Hermes. |
| KNOW-005 | Per-user instructions | P1 | `internal/chat/personality.go` | Antares | `/api/me/personality` | `POST /v1/runs` `instructions` | TESTED | `TestPersonalityAppliesToTheOwnersNextRunOnly`, `TestPersonalityInputIsStrict` |
| KNOW-006 | Memory edits confirmed by read-back | P1 | — (`docs/runbooks/memory-skills.md`) | Hermes upstream | no edit offered | no memory write route in `/v1/*` | UPSTREAM_BLOCKED | Probed the route table (every `/v1/*` and `/api/*` path): no memory write route, so no edit is offered (nothing optimistic exists). Route table: `docs/hermes/fixtures/hermes-route-probe.json`. |
| KNOW-007 | Gaps marked with evidence | P1 | this table, `internal/parity` | Antares | this table and `docs/runbooks/memory-skills.md` | n/a | TESTED | `TestCrossUserThreatNotYetBuilt` (memory surface unrouted), `internal/parity/parity_test.go` checks this table. `TestTableIsComplete` |
| IMPORT-001 | Optional read-only external import | P2 | — (#116) | Hermes upstream | — | `POST /api/sessions` creates empty sessions only | UPSTREAM_BLOCKED | Probed `/api/sessions` and `/v1/runs` `conversation_history`; no per-user source with consent exists. #116. Route table: `docs/hermes/fixtures/hermes-route-probe.json`. |
| IMPORT-002 | Non-destructive import into a new chat | P2 | — (#116) | Hermes upstream | — | no seeded-session route at `/api/sessions` | UPSTREAM_BLOCKED | Probed `/api/sessions` (`_handle_create_session`): no way to create a session from supplied messages. #116. Route table: `docs/hermes/fixtures/hermes-route-probe.json`. |

## §29 — acceptance tests (LLM-ACC)

| ID | Requirement | Pri | Source module | Owner | Antares contract | Hermes v0.22.5 | Status | Evidence |
|---|---|---|---|---|---|---|---|---|
| LLM-ACC-001 | A and B see only their own models; B cannot request A's | P0 | `internal/auth/routes.go`, `internal/chat/service.go` | Antares | server-bound profile | `/p/{profile}` | TESTED | `TestClientSuppliedProfileKeyAndSessionNeverReachHermes`, `TestModelSettingsAreScopedToTheOwner` |
| LLM-ACC-002 | Model + effort + tier persist per chat and are sent | P1 | `internal/chat/modelprefs.go` | Antares | chat settings | run body | TESTED | `TestModelChoiceIsPerChatValidatedAndSent` |
| LLM-ACC-003 | Hiding is per user; policy wins | P1 | `internal/chat/models.go` | Antares | model visibility | n/a | TESTED | `TestModelVisibilityIsPerUserAndPolicyWins` |
| LLM-ACC-004 | Context percentage sound; unknown shown | P1 | `internal/chat/usage.go` | Antares | usage view | run `usage` | TESTED | `TestUsageOccupancyIsExplicitlyUnknownNeverZero` |
| LLM-ACC-005 | Compacted chat keeps all turns | P1 | `internal/chat/history.go` | Antares | paged history | session messages | TESTED | `TestAcceptanceThousandTurnCompactedSearchAndPaging` |
| LLM-ACC-006 | Badges accurate; image input only where supported | P1 | `internal/chat/media.go` | Antares | catalogue-checked | n/a | TESTED | `TestImageSendRefusedWhenVisionIsNotConfirmed`, `web/src/ModelPicker.test.tsx` |
| LLM-ACC-007 | Generated media owned and private | P1 | — (#168) | Hermes upstream | — | `/v1/runs` raw `MEDIA:` paths | UPSTREAM_BLOCKED | Probed `/v1/runs` and `/v1/artifacts/download/{artifact_id}`. Staged assets are owner-only: `TestImageCrossOwnerDenied`. #168. Route table: `docs/hermes/fixtures/hermes-route-probe.json`. |
| LLM-ACC-008 | Voice to editable transcript, sent deliberately | P1 | `web/src/Dictation.tsx` | Antares | browser dictation into the draft | no STT route in `/v1/*` | IMPLEMENTED | `web/src/Dictation.test.tsx` (final text into the draft, never sent). Real microphone: #174. |
| LLM-ACC-009 | OpenUI turn, toggle off is Markdown, malformed is safe | P1 | `web/src/openui/*`, `internal/chat/personality.go` | Antares | safe renderer + per-chat toggle | `instructions` | TESTED | `TestVisualizeIsPerChatAndReachesOnlyItsRuns`, `web/src/openui/openui.test.tsx`, `web/src/openui/actions.test.tsx` |
| LLM-ACC-010 | Queue order, one dispatch, pause on stop | P1 | `internal/chat/queue.go` | Antares | queue | n/a | TESTED | `TestQueuedMessageIsSentOnceAfterTheRunCompletes`, `TestCancelPausesTheQueue`, `TestQueueIsOwnerScoped` |
| LLM-ACC-011 | Own memory/notes/skills only; writes upstream-blocked | P1 | `web/src/MemorySkills.tsx` | Hermes upstream | skills read-only; no memory route | no memory route in `/v1/*` | UPSTREAM_BLOCKED | Probed the route table (every `/v1/*` and `/api/*` path): no memory or notes route; skills only (`/v1/skills`). `TestCrossUserThreatNotYetBuilt`, `web/src/MemorySkills.test.tsx`. Route table: `docs/hermes/fixtures/hermes-route-probe.json`. |
| LLM-ACC-012 | Personality only for A, lower priority | P1 | `internal/chat/personality.go` | Antares | run `instructions` | ephemeral system prompt after the profile prompt | TESTED | `TestPersonalityAppliesToTheOwnersNextRunOnly` |
| LLM-ACC-013 | Unavailable balance shown as Unavailable | P1 | `internal/kenari/account.go` | Antares | Kenari usage admin view | not in Hermes | TESTED | `TestKenariUsageOutageIsUnavailableNotZero`, `TestKenariUsageNotConfiguredShowsNothing`, `web/src/ProviderUsage.test.tsx` |
| LLM-ACC-014 | No external history without consent | P2 | — (#116) | Owner | nothing routed | `/api/sessions` | OWNER_ACCEPTED_EXCEPTION | No import exists (#116 blocked), so nothing external can appear: `TestCrossUserThreatNotYetBuilt`. Holds by absence until a consented source exists. |
| LLM-ACC-015 | No profile mounts, Hermes DB reads, media pipeline, provider calls | P0 | `internal/redact`, `deploy/`, `internal/webui` | Antares | source and image checks | n/a | TESTED | `TestBrowserReadModelsCannotCarryCredentials`, `TestEveryLogPathGoesThroughTheRedactor`, `TestInstallerEnforcesDirectoryContract` |
| LLM-ACC-016 | Share, push, sign-out-all, appearance, updates, search | P1 | `web/src/*` (as the UXP rows) | Antares | as UXP rows | n/a | TESTED | `web/src/pwa.test.tsx`, `web/src/Notifications.test.tsx`, `web/src/SignOutEverywhere.test.tsx`, `web/src/Settings.test.tsx`, `web/src/Search.test.tsx` |

## Hermes probe summary (v0.22.5)

| Area | Observed |
|---|---|
| Capabilities | `GET /v1/capabilities` (fixture `docs/hermes/fixtures/capabilities.body.json`) |
| Model options | `GET /api/model/options`; provider quota is not part of it |
| Sessions and messages | `/api/sessions`, `/api/sessions/{id}/messages`, fork; create is empty only |
| Runs | `/v1/runs`, events, steer, approval, clarify, stop, resolve-unknown; `instructions` accepted |
| Multimodal input | `image_url` data URLs in run input |
| Artifacts | `/v1/artifacts/upload`, `/v1/artifacts/download/{id}`: one-shot, browser-control only |
| Memory, notes | no route |
| Skills | `GET /v1/skills` read-only list |
| Jobs | `/api/jobs*` per profile, cron run honours the profile's disabled toolsets |
| Provider quota | no route |
| Voice STT/TTS | no route |

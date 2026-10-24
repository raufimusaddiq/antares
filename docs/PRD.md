# Product Requirements Document (PRD)

# Hermes Chat UI — Google Login, Admin Approval, Private Memory & OpenUI

| Metadata | Value |
|---|---|
| Document | **Chat application requirements only** |
| Version | **2.4** — mature Aixell-class Chat UX parity, PWA/push, search, voice, editor, turn controls, export, budgets and tests |
| Date | 21 October 2026 |
| Status | **Ready for implementation planning; security gates block production until verified** |
| Target | Self-hosted, responsive browser application |
| Stack | **Go backend + React 19 / TypeScript frontend + OpenUI Lang** |
| Deployment | **Two existing/intended Docker services:** `hermes` (already deployed) and `chatui` (new), on the same edge / reachable private Docker network |
| AI authority | **Hermes only**: models, agent reasoning, memory, tools, skills, runs, tool approvals |
| Scope | **Chat UI application and its required integration contract**; no Hermes rebuild or new agent runtime |
| Product topology | One logical assistant in the UI; independently scoped per-user Hermes execution/state |

> **Critical security constraint:** Logging in to Google and passing different chat IDs does **not** provide private Hermes memory. Each approved user MUST be mapped to a dedicated Hermes profile (or demonstrably equivalent independently isolated state), and tools must not be able to read another user's profile. `X-Hermes-Session-Key` helps long-term memory providers scope their recall but is not a substitute for profile isolation. If the existing Hermes deployment does not satisfy these preconditions, the app MUST refuse to enable chat for affected users rather than silently using the default/shared profile.
>
> **Topology constraint:** Keep the existing Hermes container and add one `chatui` container. Do **not** mount Docker socket, Hermes data volume, or Hermes credentials into the public web frontend. A manual operator provisioning step in the existing Hermes container is acceptable for v1; an automatic provisioning mechanism is **not assumed** to exist in the Hermes HTTP API.

---

## 1. Executive summary

Build a lightweight, high-quality Chat UI in front of an **already deployed Hermes Agent**. Visitors authenticate with Google OpenID Connect (OIDC). A successful Google sign-in creates a **pending account**, not permission to converse. An administrator must approve the user and ensure that a private Hermes profile is bound and ready; only then may the user open, resume, or create chats.

The app must deliver a **mature personal chat product** (Aixell-class interaction quality), not only a basic chat composer: installable PWA, private push notifications, full-text conversation search, voice/dictation where supported, advanced Markdown composer, personal templates and per-chat drafts, context/token/estimated-cost tracking, safe turn controls, sandboxed artifact previews, and Markdown/PDF export. These capabilities are defined as normative requirements in §§21–25 with feature-specific acceptance tests. Original security/tenant constraints remain mandatory.

The app serves a single logical assistant and supports the Hermes capabilities available in its deployed version: model selection, streaming turns, native tools, skills, web search, image and video generation, speech tools, memory, approvals, and optionally jobs. It must **not** recreate LLM/tool orchestration or call LLM providers directly. Interactive answers may be rendered as **Thesys OpenUI Lang** inside React chat, with secure action handling and safe plain-text/Markdown fallback.

The new `chatui` Docker container hosts the Go API, static frontend assets, and an embedded SQLite database. Hermes remains in its own container with its own persistence. The containers communicate over a trusted private network; the browser talks **only** to Chat UI.

**Definition of success:** An approved user gets a mature chat experience comparable in interaction depth to Aixell **without** compromising strict per-user authorization. An unapproved user cannot make Hermes perform any work. An approved user sees only their own transcripts, memories, generated files, runs, tasks, and tool outcomes. An admin can approve/revoke access without gaining default access to private chat content. Existing Hermes functionality remains intact.

### 1.1 Explicit assumptions

- "Same edge" is interpreted as **the same edge host or a network in which `chatui` can privately reach `hermes`**. Exact Docker network name, DNS alias, and reverse-proxy setup are deployment inputs, not facts established by this document.
- Hermes is **already running**, but its exact version, active API endpoints, multi-profile configuration, existing profiles, and security settings are **not yet verified**. Implementation must probe these.
- The app owner has operational access to configure/prepare user profiles in the existing Hermes container. This does not give the Chat UI permission to execute Docker commands.
- One logical assistant means a shared product identity and capabilities, **not a shared Hermes memory directory**.
- This PRD uses **Go + React/TypeScript** to match the established stack preference; no Python in the Chat UI application.

### 1.2 Non-negotiable principles

1. **Default deny:** no approved + healthy tenant binding → no Hermes API request.
2. **Separation of duties:** Google authenticates identity; Chat UI authorizes users; Hermes runs LLMs/tools.
3. **Backend selects profile:** users cannot supply profile names, Hermes route prefixes, API keys, or session keys.
4. **Private data by construction:** session ownership, credential scope, job ownership, asset ownership, tool execution boundaries.
5. **No agent clone:** all AI reasoning and generative UI markup come from Hermes.
6. **Capability-driven UI:** no unsupported or unconfigured feature displayed as available.
7. **Small footprint:** two containers, SQLite, no Redis/Kafka/Postgres/third orchestration service in v1.
8. **Failure must be visible:** never present a failed/cancelled Hermes run as completed.

---

## 2. Ownership and architecture

### 2.1 Responsibilities

| Component | Owns | Does **not** own |
|---|---|---|
| Google OIDC | Identity assertion, account `sub`, verified email | Chat authorization, Hermes identity |
| Chat UI frontend (React) | Login UX, pending screen, chat, streaming display, OpenUI renderer, admin interface | Hermes credentials, permission decisions, tool execution |
| Chat UI backend (Go) | App sessions, approval state, RBAC, profile binding, conversation/run ownership, SSE gateway, file access, audit, quotas | LLM inference, agent memory semantics, native Hermes tool orchestration |
| Chat UI SQLite | Users, approvals, profile bindings, app session hashes, chat/run mappings, assets metadata, audit log | Canonical Hermes memory and entire transcripts |
| Hermes (existing container) | Agent execution, provider/model settings, native tools/skills, memory, Hermes sessions/runs/jobs | Google sign-in, registration approvals, app-level RBAC |
| Caddy/edge proxy (existing) | TLS, route to Chat UI, upstream network boundary | Hermes user authorization |

### 2.2 Required data flow

```text
Browser (only HTTPS origin)
  │
  │ Google OIDC login / Chat UI API / SSE
  ▼
Existing Edge Reverse Proxy
  │
  ▼
┌──────────────────────────────────────────────────┐
│ chatui container                                 │
│  Go HTTP API + auth/RBAC + Hermes adapter        │
│  React static assets + OpenUI                    │
│  SQLite WAL + private asset volume               │
└───────────────────┬──────────────────────────────┘
                    │ private Docker network ONLY
                    │ per-user Hermes API key and profile binding
                    ▼
┌──────────────────────────────────────────────────┐
│ existing hermes container                       │
│  profile A -> own memory/sessions/skills         │
│  profile B -> own memory/sessions/skills         │
│  models/tools/provider API calls executed here   │
└──────────────────────────────────────────────────┘
```

**Forbidden:** `Browser → Hermes`, `Browser → external LLM provider`, shared chat ID without per-user binding, Hermes volume mounted in `chatui`, Docker socket mounted in `chatui`, hard-coded all-users API key in frontend.

### 2.3 Small-footprint deployment contract

- **One new container** `chatui`: Go binary serves built React app and JSON/SSE API on one internal port; includes SQLite driver.
- **One existing container** `hermes`: not recreated or overwritten by this project.
- **One private persistent volume** for Chat UI app DB and assets. Hermes keeps its own volume; do not cross-mount it.
- Reuse **existing** Caddy/reverse proxy; no additional Caddy, auth proxy, database, worker, queue, or cache required by Chat UI.
- Local Go worker goroutines may perform SSE relay, periodic cleanup, health checks, and retry reconciliation in the same process.
- Explicit resource limits and upload/stream concurrency; no unbounded in-memory aggregation of model streams or media.

---

## 3. User roles, registration, and admin approvals

### 3.1 Roles

| Role / state | Login | Pending page | Chat | Own chat/history/media | Admin users/approvals | Other users' chat content |
|---|---|---|---|---|---|---|
| Anonymous | No | No | No | No | No | No |
| Pending | Yes | Yes | No | No | No | No |
| Rejected | Yes | Rejected page | No | No | No | No |
| Approved, profile not ready | Yes | Setup pending | No | No | No | No |
| Active user | Yes | N/A | Yes | Yes | No | No |
| Suspended / revoked | Login may succeed | Blocked page | No | No via UI | No | No |
| Admin (active and mapped) | Yes | N/A | Yes | Yes, **only own** | Yes | No by default |

**Role ≠ status.** `role = USER | ADMIN`; `approval_status = PENDING | APPROVED | REJECTED | SUSPENDED`; `tenant_status = UNPROVISIONED | CONFIGURING | READY | FAILED | DISABLED`. Chat eligibility is a predicate, not merely `approved = true`.

### 3.2 Hard eligibility rule

```text
can_chat(user) =
  authenticated_session_valid
  AND user.approval_status == APPROVED
  AND user.tenant_status == READY
  AND user.hermes_profile_binding != null
  AND hermes_binding_healthy
  AND policy_not_suspended
```

Each chat, history, upload, run, job, SSE subscription, download, and OpenUI action must apply this guard as appropriate. Pending/rejected/suspended users may access only the minimal account-status/logout endpoints. Admin approval endpoints also require `role == ADMIN` and valid active session.

### 3.3 Registration and approval lifecycle

```text
Google sign-in
  ├─ existing active account -> Chat
  ├─ existing suspended/rejected -> Blocked page
  └─ new Google sub -> PENDING -> Pending approval page
                                  │
                       admin APPROVE / REJECT
                                  │
                      APPROVE -> tenant setup/bind
                                  │
                     READY + checks passed -> Chat
                                  │
              admin SUSPEND / REVOKE -> blocked
```

**Requirements (P0):**

- `AUTH-001`: Google OIDC Authorization Code flow with PKCE; server verifies ID token signature/JWKS, issuer, audience, expiry, `nonce` and anti-CSRF `state`; checks `email_verified`.
- `AUTH-002`: Durable identity key = `(google_issuer, google_sub)`; **email is display metadata only**, not authorization key.
- `AUTH-003`: First login creates `PENDING`, `USER`, `UNPROVISIONED`; does not trigger Hermes.
- `AUTH-004`: Show pending page explaining that admin approval is required; include identity email/name, status, logout, refresh; **no chat composer, stream, chat metadata, tools or asset endpoints**.
- `AUTH-005`: Admin can view pending users (email, name, date requested), approve, reject, suspend, re-approve, and see provisioning status. Actions require a reason for rejection/suspension and persist actor/time.
- `AUTH-006`: Approval changes access **only after tenant profile binding is READY**. Admin can approve before profile is prepared; users then see `SETTING_UP` rather than Chat.
- `AUTH-007`: Login sessions use random opaque server-side tokens, hashes stored in DB; `Secure`, `HttpOnly`, `SameSite=Lax` cookies, with CSRF protection and session rotation at privilege changes.
- `AUTH-008`: Admin is bootstrapped **out of band**. Require one-time CLI/config operation that promotes a known Google `sub` after successful Google login. Never auto-promote first visitor, same-domain users, or arbitrary approved emails.
- `AUTH-009`: Recheck status server-side for every privileged request and stream attachment; no positive authorization cache surviving revocation.
- `AUTH-010`: Revoke invalidates active app sessions, closes/disallows SSE, denies new turns and scheduled job management; attempts best-effort run cancellation and job pause; failures generate an alert. Already-executed tool side effects cannot be undone automatically.
- `AUTH-011`: Reject and suspend do not delete private data unless a separate audited delete action is confirmed. Reapproval preserves prior private state if it is still present.
- `AUTH-012`: Administrative actions (especially role changes) must use independent server authorization checks, not a user-editable field in JSON or frontend route guards.

### 3.4 Admin UX (P0)

- `/admin/users`: searchable/paginated directory (substring search on name/email and the approval filter run server-side; the tenant-failed filter applies to loaded rows because the API cannot filter on it), creation/last-login times. A pending count is not shown (#47 follow-up).
- `/admin/users/:id`: public Google identity metadata, approval history, tenant binding status, last Hermes connectivity check, quota status. **No private chats, transcripts, memories, file previews, or raw tool arguments.**
- Explicit `Approve`, `Reject`, `Suspend`, `Reactivate`; destructive actions use confirmation dialog and optional reason.
- `/admin/system` — **deferred, not built** (#47 removed the 501 stub; the route does not exist and answers 404 for an admin). When real data exists it returns as its own ticket, not as a placeholder. Intended content: Hermes connectivity, version/capability snapshot, active user count, run counts, failed provisioning/streaming, disk and app health. Redact secrets.
- Audit feed of approval, user status, admin login, tenant binding, key rotation, and policy changes.
- For any recovery requiring viewing private content, design a distinct audited support-access workflow **out of scope for v1**. Admin power alone is not consent.

### 3.5 Admin acceptance tests

- Unknown Google user signs in and only sees pending page; direct `POST /api/chats` returns `403 ACCOUNT_PENDING` and creates no Hermes run.
- Admin approves a user with no Hermes profile; user stays blocked with `TENANT_NOT_READY` until binding passes checks.
- User cannot promote self by editing `role`, `status`, `profile`, a query param, or calling admin endpoints.
- Revoked user cannot reconnect to old SSE URL, download old assets, send a continuation, run OpenUI actions, or invoke jobs.
- Two admins updating the same user simultaneously produce deterministic state transitions and an audit trail.

---

## 4. Google authentication and sessions

### 4.1 Minimal Google scopes

- Request **only** `openid email profile` for login.
- Google Gmail/Drive scopes are **not** required and not requested by default. If Hermes tools integrate those services later, authorization occurs independently through Hermes/provider configuration.
- Use a Google OAuth web client with exact redirect URI and HTTPS public origin.

### 4.2 Browser session security

- Backend sets an opaque session cookie; store only SHA-256/strong digest in SQLite, never plaintext cookie tokens.
- Rotate session at login, role escalation and explicit sensitive account changes. On logout, invalidate DB session; clear cookie.
- Session idle/absolute expiration configurable; sane defaults documented in configuration (not represented as Google expiry).
- CSRF protection for all cookie-authenticated mutating methods; validate `Origin` for state changes and SSE initiations as applicable.
- Rate-limit Google login callbacks and sensitive admin mutations per user/IP; no user-enumerating public endpoints.
- Enforce CSP, `Referrer-Policy`, `X-Content-Type-Options`, HSTS through edge, secure response caching (`Cache-Control: no-store`) on private API data.
- Never persist Google access/refresh tokens unless a separate feature specifically needs them; the ID token is used to establish **app** identity.

---

## 5. Hermes tenancy and memory isolation — release blocker

### 5.1 Per-user private Hermes binding

Every active user is mapped to exactly one active **Hermes profile** and one server-side credential reference. Use synthetic profile slugs (e.g. `u_<opaque_id>`, not email/name). Profile names, credentials, memory IDs, and upstream URLs are never user-configurable.

A binding record contains:

- `app_user_id` (immutable foreign key)
- `hermes_profile` (unique among current users)
- `routing_mode` (`MUX_PREFIX` or `DEDICATED_PORT`)
- `upstream_base_url` (private, validated against allowlisted internal endpoints)
- `credential_ref` (secret reference, never browser-visible)
- `memory_scope_key` (deterministic internal mapping, e.g. `chatui:user:<uuid>`)
- `provisioned_at`, `last_health_check`, `verified_at`, `status`

**Never reuse** the same Hermes profile, memory scope, session ID, asset namespace, or bearer key across users. Database unique constraints MUST enforce these relationships.

### 5.2 Supported Hermes routing options

**Option A — Multiplex profile routing (preferred if verified in deployed build):**

- Hermes supports `gateway.multiplex_profiles`; requests use `http://hermes:8642/p/<profile>/...` with **the profile-specific `API_SERVER_KEY`**.
- The `chatui` backend derives path and key from authenticated user binding; the frontend sends neither.
- Current Hermes documentation describes per-profile key enforcement and profile-scoped run routes; confirm the deployed image/feature is present before enabling.
- Do **not** send the default profile's bearer key to a named profile route. Hermes fixed an earlier cross-profile key-acceptance behavior in July 2026; this must be covered in preflight tests.

**Option B — A dedicated API port per profile (compatibility fallback):**

- Profiles run inside the existing Hermes container with distinct `API_SERVER_PORT` values and distinct credentials. The Chat UI uses a private `hermes:<port>` endpoint for the relevant binding.
- No new Chat UI container is required, but the existing Hermes container **may require internal profile/port configuration changes** by its operator. If an unreachable port or binding is not provisioned, user stays blocked.
- For either option, profile preparation is an **operator dependency**, not an implicit Chat UI feature.

### 5.3 Provisioning and approval integration

The Chat UI must not invoke `docker exec`, mount `/var/run/docker.sock`, write Hermes `.env`, or mutate Hermes files from a public web request.

**v1 supported workflow:**

1. New Google account enters `PENDING`.
2. Admin approves; account becomes `APPROVED + UNPROVISIONED`.
3. Trusted Hermes operator creates/starts a private profile **inside the existing Hermes container**, sets its profile-specific API key, ensures isolated storage/tool policy, and makes it reachable privately.
4. Admin enters/binds the profile identifier and configured internal credential reference using a protected admin operation (or a trusted CLI bootstrap). Never paste secrets into public URLs or logs.
5. Chat UI runs readiness tests: endpoint response, profile identity, credential isolation, session ownership, selected tool policy, memory boundaries.
6. Only on successful validation does `tenant_status` become `READY` and chat unlock.

**Amended 2026-10-22 (owner decision, issue #18):** automated provisioning is now **in scope**. It is delivered by a separately authenticated, narrowly scoped host-side helper (`tools/hermes-provisioner/`) that drives `hermes profile create/delete/purge-identity` over `docker exec`; Antares calls it over HTTP with a bearer token and never receives Docker access. Hermes itself still exposes no provisioning REST endpoint, and none is to be invented.

### 5.4 Memory contract

Hermes has multiple distinct state concepts:

1. **Profile state:** local Hermes memory, `SOUL.md`, skills, API configuration, sessions, jobs, etc. This is the primary user separation boundary.
2. **Transcript session:** each conversation uses its own Hermes `session_id`, stored in the profile's session DB.
3. **Long-term provider scoping:** `X-Hermes-Session-Key` is a stable identifier across conversations and is documented for integrations such as Honcho; it does not isolate all filesystem/profile state.

Requirements:

- `MEM-001`: One profile per active user; new user never uses shared `default` profile unless it is reserved exclusively to that one user.
- `MEM-002`: Generate one stable, **server-derived** `X-Hermes-Session-Key` per user (not chat) when invoking supported `/v1/runs`/`/v1/responses` flows; max 256 characters and no control characters.
- `MEM-003`: Separate Hermes `session_id` per chat. Conversation IDs are opaque and never cross user/profile boundaries.
- `MEM-004`: User-created memory persists across their own chats if the configured Hermes memory provider supports it. It must **not** be manually copied across profile directories.
- `MEM-005`: Users may inspect/remove own memory only if a safe, documented Hermes capability is available. Do not claim a memory-management UI exists without an integration endpoint.
- `MEM-006`: Block chat when memory/session storage is misrouted or a profile identity mismatch is detected.
- `MEM-007`: For deletion, invoke supported session deletion and run/storage cleanup; coordinated removal of Hermes profile data is an operator-controlled step if no safe upstream API exists. Mark deletion pending until complete.

### 5.5 Threat model: profiles are not sandboxes

**Warning:** Hermes profile isolation segregates memory/state but **does not automatically prevent shell/file/browser tools from reading other directories mounted inside the same Hermes container**. Two profile folders can coexist under the existing Hermes data volume. A malicious user prompt or compromised tool may try to open sibling profile files.

Security requirements for shared-container multi-user operation:

- Default-deny unsafe terminal/shell, unrestricted file read/write, arbitrary Python execution, external host mounts, and credential-exposing tools for ordinary users **until a sandbox boundary is verified**.
- Never attach host Docker socket or sensitive host paths to Hermes merely to support UI features.
- For tools that require filesystem access, enforce profile-specific effective UID/permissions/allowed paths or a genuinely isolated tool sandbox with inaccessible sibling state; verify with cross-user read/write tests, not prompt instructions alone.
- Prevent provider credential leakage in tool outputs, error messages, logs, or OpenUI data.
- No shared Honcho identity/scope, external vector memory collection, global file index, or shared private-search namespace.
- Treat model prompts, retrieved pages, tool outputs, and generated UI as untrusted; prompt instructions do not replace access controls.

**Production rule:** If unrestricted local tools are a firm requirement for untrusted users, a single Hermes container with only profiles is **not demonstrated to provide hard adversarial isolation**. Either disable such tools for those users or revisit the constraint to allow per-user OS/container boundaries. This PRD does not promise zero leakage where an upstream execution boundary is absent.

### 5.6 Required cross-user negative tests (P0, blocking)

With independently approved test users A and B:

- A's memory writes recall for A after logout/new chat; B never recalls them.
- B cannot access A's chats by guessed IDs, sessions API forwarding, raw upstream IDs, history search, SSE subscriptions, cancellation, tool approvals, jobs, or asset URLs.
- B using A's `session_id`, `run_id`, or `X-Hermes-Session-Key` **as client-supplied input** cannot change the server-derived binding.
- B cannot read A's profile memory/SQLite files through exposed Hermes tools; unsafe tools stay disabled if boundary is unverified.
- A/B simultaneous streaming and profile transitions do not cause cross-user SSE events or session adoption.
- Invalid per-profile key fails closed; default profile credential must never authorize another profile.
- Removing the profile mapping prevents all Chat UI → Hermes calls for that user.

---

## 6. Hermes integration adapter (Go)

### 6.1 Adapter responsibility and boot probing

Implement `internal/hermes` behind typed Go interfaces. No frontend direct Hermes URLs or tokens. On boot and periodically:

- Probe `/health` and **authenticated** `/v1/capabilities` for a test/admin binding.
- Discover `/api/model/options` or `/v1/models`, `/v1/skills`, `/v1/toolsets` as advertised.
- Verify streaming, event semantics, session CRUD, runs polling, approval controls, model overrides, jobs support, and profile routing for the **actual deployed version**.
- Maintain capability flags and block/disable UI controls when unavailable. A plain `/health = ok` is not evidence of tenant readiness.
- Use finite connection/read timeouts, bounded buffers, typed JSON, and streaming backpressure.
- Upstream URLs are constructed from server-controlled allowlisted network targets; never accept client-supplied URLs (SSRF defense).

### 6.2 Preferred upstream chat lifecycle

1. Backend creates an owned app chat and Hermes session under the user's profile.
2. Backend inserts `(user_id, app_chat_id, hermes_session_id, profile_binding_id)` atomically after successful upstream session creation or records recovery state.
3. On send, backend authorizes chat, validates profile ownership, creates app-side run admission/idempotency ID.
4. Backend calls profile-scoped `POST /v1/runs` with `session_id`, `input`, optional supported model/options, stable **server-derived** `X-Hermes-Session-Key`, and `Idempotency-Key`.
5. Backend maps `hermes_run_id` to app run and subscribes to `GET /v1/runs/{id}/events`, relaying normalized SSE to browser.
6. Browser refresh/reconnect uses app run ID; backend reauthenticates/rechecks owner and polls Hermes `GET /v1/runs/{id}` to reconcile terminal status.
7. Final status comes from **Hermes terminal run state** (`completed`, `failed`, `cancelled`, `interrupted` or other documented states), not merely receiving assistant text.

Use documented `/api/sessions/{id}/chat/stream` only as an explicitly tested fallback with compatible session/memory semantics. Do not assume the `X-Hermes-Session-Key` header is supported on every endpoint; its documented support includes `/v1/runs`, `/v1/responses`, and Chat Completions. Follow the endpoint map in §9A and bind conversation identity only to an API-server-owned session (never a CLI/TUI/messaging-owned session).

### 6.3 Client-facing normalized stream

All events are sent on the **Chat UI origin**, not Hermes:

```text
event: run.started
data: {"runId":"app-run-id","chatId":"app-chat-id"}

event: assistant.delta
data: {"text":"..."}

event: tool.started
data: {"toolCallId":"...","label":"Web search"}

event: tool.completed
data: {"toolCallId":"...","summary":"..."}

event: approval.required
data: {"runId":"...","approvalId":"...","choices":["once","deny"]}

event: run.completed
data: {"runId":"...","status":"completed","usage":{}}
```

These are **Chat UI's normalized event names**, not claims that all Hermes endpoints emit the same fields. Maintain an event adapter and explicit mapping tests against recorded upstream fixtures. Maintain monotonic app-local event sequence numbers and `Last-Event-ID`/resume strategy using bounded per-run buffers or persisted event checkpoints; if history is unavailable, poll/reconcile and reload canonical transcript instead of duplicating assistant turns.

### 6.4 Supported Hermes features

| Feature | UI requirement | Source of truth | Priority |
|---|---|---|---|
| Chat + streaming | Streaming composer, tool progress, cancel/retry | Hermes runs/sessions | P0 |
| Model selection | Available provider/model + reasoning options if exposed | `/api/model/options`, `/v1/capabilities` | P1 |
| Chat history | New, rename, list, resume, delete, pagination | Hermes sessions + app ownership index | P0 |
| Long-term memory | Recall/private memory | Hermes profile + supported memory scope | P0 |
| Web search/tools | Show actual tool state and sources | Hermes toolsets and run events | P0 |
| Images/vision | Inline image input, generated asset gallery | Hermes configured tools/API capabilities | P1 |
| Video generation | Async progress and video playback | Hermes configured tool/provider | P1 |
| TTS/audio | Audio playback; speech inputs only if supported | Hermes configured capabilities | P1 |
| Skills | Read-only skill list (admin controls availability outside app) | `/v1/skills` | P1 |
| Approvals/clarify | Per-run consent UI; fail closed on unknown controls | Hermes run-control endpoints | P1 |
| Scheduled jobs | User-scoped CRUD/status, pause on suspension | Hermes jobs API | P1 (gated) |
| Usage metadata | Own model/tokens/status when available | Hermes run status | P1 |
| Unsupported tools | Hidden/disabled with truthful reason | Capability probing | P0 |

"Support everything Hermes supports" means **pass through enabled, exposed features through a secure adapter**, not invent a UI for undocumented or privileged internals. Tools need not be separately reimplemented or exposed for clients to call by name.

### 6.5 Tool call and model controls

- Hermes is free to select native installed tools according to its config and policy; Chat UI never fabricates tool success.
- UI must show progress states: requested / started / succeeded / failed / waiting for consent, with readable errors.
- Model overrides are allowed only for admin-approved provider/model choices; user-supplied arbitrary `provider`, `base_url`, or key is blocked.
- Tool approval requests must identify the real upstream pending approval; only the owning user may approve/deny their own run, using validated exact prompt/generation IDs where required.
- Revoked accounts may not approve already pending tool actions; try to cancel the upstream run and log failure if cancellation cannot be completed.
- Support model/provider defaults at tenant/profile level, without changing Hermes global config from chat input.

---

## 7. OpenUI Lang inside the Chat UI

### 7.1 Exact technology

Use **OpenUI Lang by Thesys**, not arbitrary generated HTML or an undocumented JSON UI format. The frontend uses `@openuidev/react-lang` (Renderer) with `@openuidev/react-ui` or a specifically defined `createLibrary` component registry. Pin a compatible dependency set (React 19; check documented peer dependencies and the installed package versions).

Docs: https://www.openui.com/docs/openui-lang/renderer and https://www.openui.com/docs/openui-lang/architecture

### 7.2 Presentation pipeline

```text
User sends request to Chat UI
  -> Go sends user text / validated UI instructions to Hermes
  -> Hermes produces plain text / Markdown OR OpenUI Lang
  -> Go relays the canonical streamed text, preserving ownership
  -> React recognizes a validated OpenUI message/segment
       -> Renderer(library=allowlist, response=..., isStreaming=...)
       -> render cards/charts/tables/forms/actions
     OR safely renders Markdown/plain text fallback
```

**Important:** Hermes does **not automatically generate OpenUI Lang** because the UI has a renderer. OpenUI's approach requires sending a component-library instruction/spec in the model context. The Chat UI backend must be able to attach a **versioned OpenUI instruction** through Hermes' documented per-request instruction/system-prompt mechanism, layered on top of Hermes' normal system prompt, while leaving Hermes in charge of the response. This instruction may be always-on for approved chats or activated for rich responses; validate real Hermes model compatibility before deciding automatic vs opt-in mode.

### 7.3 Minimum component registry

- Display: Text, Markdown, Headings, Stacks, Rows, Dividers, Cards, Badges, Images, Video previews, Audio player.
- Data: Tables, metrics, bar/line/pie charts if supported by approved registry.
- Interactivity: buttons, selection, checkboxes, structured follow-up input, compact forms.
- Tool-aware: source list, tool result summary, image/video gallery, pending approval (rendered as trusted app component, not arbitrary model action).
- Mobile-responsive layout, dark/light themes, loading/empty/error states, accessible keyboard behavior.

### 7.4 OpenUI execution security (P0)

- **Whitelist components and prop schemas**; unknown components/functions/props fail to safe fallback.
- Absolutely **no** model-provided raw HTML/JS evaluation, script injection, arbitrary iframe sources, arbitrary fetch URLs, or unconstrained dynamic imports.
- **Do not** bind OpenUI query/mutation declarations directly to Hermes or external URLs. If reactive actions are enabled, expose **only** explicitly registered Chat UI tools that enforce the same user/status/ownership/CSRF guards as normal API requests.
- A follow-up action that continues conversation becomes a normal authenticated message **in the user's current chat**. Destructive actions require explicit user confirmation and safe backend validation.
- Bound size of generated UI markup, nesting depth, action arguments, assets, and streaming chunks; limit the renderer's work during frequent deltas.
- Sanitize Markdown links and media sources. Private media loads only through authenticated/short-lived owned URLs.
- Model generated UI is **presentation**, never authorization evidence, never a valid tool approval in itself.
- Store canonical response text in Hermes transcript; app may maintain a rendering manifest/version. On old content or incompatible renderer, show safe Markdown/raw text, not a broken/blank message.

### 7.5 OpenUI acceptance tests

- Request "compare 3 options in a table" → Hermes may return OpenUI table; renderer displays correctly when markup valid, Markdown otherwise.
- Streaming incomplete OpenUI Lang does not freeze/crash React; final UI renders without inventing values.
- Malicious OpenUI tries to invoke `/admin/*`, fetch another user's asset, or use an unregistered mutation → rejected by backend and UI with no side effects.
- Form action from A's chat cannot submit into B's chat even if Chat ID is modified in browser requests.
- Tool outputs containing HTML/script payloads render inertly, including in Markdown mode.
- Keyboard, mobile, desktop, dark/light modes tested.

---

## 8. Chat UI: pages and user experience

### 8.1 Public/auth pages

| Page | Access | Required functionality |
|---|---|---|
| `/login` | Anonymous | Google sign-in, privacy note, no local password field |
| `/pending` | Pending | Status, request date, logout, refresh; no conversation controls |
| `/rejected` | Rejected | Informative access denied reason if policy allows; no chats |
| `/setup` | Approved but not ready | "Account approved; private workspace being prepared" |
| `/suspended` | Suspended | Access disabled with no private data rendering |

### 8.2 Authenticated chat pages

**Product parity is mandatory:** implement the detailed user journeys, UI elements, API, data and acceptance tests in §§21–29. In particular, chat search must include message bodies (not only conversation titles), run completion can trigger an opt-in push notification, and a previously opened conversation must recover its rendered turn sequence after reload or restart.

- Sidebar: New chat, recent conversations, search (own only), rename, delete, session status; compact on mobile.
- Main: rich assistant bubbles, streaming text, Hermes tool progress, citation/source rendering when provided, attachments preview, OpenUI, audio/video/media gallery.
- Composer: enter-to-send with configurable shift-enter, cancel run, retry failed send, own chat mode, image input when supported.
- Header: current configured model, selected run state, capabilities/tools panel, user avatar/account/logout.
- User settings: display preferences, theme, chat retention/deletion, model preference where permitted, help/support; **not** raw provider API keys by default.
- Accessibility: semantic controls, reduced-motion preference, screen reader announcements for run state, focus management, mobile keyboard ergonomics.
- Errors: expired session reauth, Hermes offline, rate limit, approval waiting, run cancelled/failed, tenant setup incomplete; no false success.
- **Performance UX (P0/P1):** Initial chat loads only the most recent bounded turn page; scrolling upwards fetches older turns with a stable cursor. Long conversations use virtualized/message-window rendering and preserve scroll position. Media loads on viewport demand; heavyweight OpenUI renderers are lazy-imported. Detailed contracts and tests: §14.1–§14.2.

### 8.3 Admin pages

Separate admin nav and account approval operations as section 3. Admin should be able to operate from mobile but prioritize correctness and dense table navigation on desktop.

### 8.4 Session continuity

- Reload / refresh retains user chat list and Hermes session association.
- Continue conversation with correct upstream profile and session; do not send client-supplied entire history as if it were trusted cross-tenant state.
- User can cancel long run; disconnecting browser does **not** automatically cancel a Hermes run unless explicitly chosen.
- When network reconnects, reconcile via backend ownership check and upstream run status; avoid duplicate turns with idempotency keys.
- **Turn-level history (P0):** reopening a chat returns all authorized historical user/assistant turns, tool result summaries when Hermes retains them, timestamps and owned assets, in deterministic order. Use `GET /api/sessions/{id}/messages?include_compacted=true&inline_images=false` upstream where the deployed build supports these options; do not silently omit archived/compacted turns. Historical OpenUI restores its validated rendering manifest or falls back to safe Markdown.
- **Pagination caveat:** Hermes documents paginated **session lists**, but does not promise native message/turn cursor pagination on the session-messages route. Chat UI owns a stable cursor contract and may use bounded per-user app-side snapshot/index/cache as required; never mislabel invented Hermes query parameters as native. A full-history read must have explicit memory, payload and timeout limits, with a deliberate fallback for large sessions.
- Refresh, logout/login, container restarts, and conversation rename must not change upstream session identity or duplicate displayed turns.

---

## 9. Chat UI REST/SSE API contract

All endpoints begin with `/api/`. Every private endpoint resolves the identity from the **secure app session** and applies RBAC + ownership. Paths use **app UUIDs**, never raw Hermes IDs.

| Method | Path | Who | Operation |
|---|---|---|---|
| `GET` | `/api/auth/google/start` | Anonymous | Start OIDC authorization |
| `GET` | `/api/auth/google/callback` | Anonymous | Validate OIDC and create app session |
| `POST` | `/api/auth/logout` | Signed in | Invalidate app session |
| `GET` | `/api/me` | Signed in | Own account/role/status + capability flags (no secrets) |
| `GET` | `/api/account/status` | Signed in | Approval and setup state |
| `GET` | `/api/chats` | Active | Own chats, pagination |
| `POST` | `/api/chats` | Active | Create owned Hermes session |
| `GET` | `/api/chats/{chatId}` | Active owner | Own metadata |
| `PATCH` | `/api/chats/{chatId}` | Active owner | Rename |
| `DELETE` | `/api/chats/{chatId}` | Active owner | Delete with upstream reconciliation |
| `GET` | `/api/chats/{chatId}/messages` | Active owner | Owned Hermes transcript proxy |
| `POST` | `/api/chats/{chatId}/messages` | Active owner | Submit run with idempotency |
| `GET` | `/api/chats/{chatId}/usage` | Active owner | Own chat tokens as Hermes reported them, estimated cost with price date, opt-in per-user token limit across all the user's chats, plus per-reply usage (off by default; `GET`/`PUT /api/me/budget`) (UXP-008/009) |
| `GET` | `/api/runs/{runId}` | Active owner | Run status |
| `GET` | `/api/runs/{runId}/events` | Active owner | SSE stream/reconnect |
| `POST` | `/api/runs/{runId}/cancel` | Active owner | Cancel upstream run |
| `POST` | `/api/runs/{runId}/approve` | Active owner | Approve or deny pending Hermes action |
| `POST` | `/api/runs/{runId}/clarify` | Active owner | Answer upstream clarification (if supported) |
| `GET` | `/api/capabilities` | Active | Enabled Hermes features, model options, toolsets |
| `POST` | `/api/assets` | Active | Allowed media upload proxy or app private asset staging |
| `GET` | `/api/assets/{assetId}` | Active owner | Stream private media |
| `GET` | `/api/jobs` | Active | Own jobs, only when P1 enabled |
| `POST` | `/api/jobs` | Active | Create scoped job |
| `PATCH` | `/api/jobs/{jobId}` | Active owner | Update/pause/resume own job |
| `DELETE` | `/api/jobs/{jobId}` | Active owner | Delete own job |
| `GET` | `/api/admin/users` | Admin | List accounts |
| `GET` | `/api/admin/users/{userId}` | Admin | Account metadata/status only |
| `POST` | `/api/admin/users/{userId}/approve` | Admin | Approve user |
| `POST` | `/api/admin/users/{userId}/reject` | Admin | Reject with reason |
| `POST` | `/api/admin/users/{userId}/suspend` | Admin | Suspend and invalidate access |
| `POST` | `/api/admin/users/{userId}/reactivate` | Admin | Re-enable approval workflow |
| `POST` | `/api/admin/users/{userId}/tenant-binding` | Admin | Register/check prepared Hermes binding via secret reference |
| `POST` | `/api/admin/users/{userId}/tenant-verify` | Admin | Run required isolation/preflight checks |
| `GET` | `/api/admin/audit` | Admin | Authorization and operation audit |
| ~~`GET`~~ | ~~`/api/admin/system`~~ | — | **Deferred, removed in #47** (no real data source; was a 501 stub). Health + availability summaries when built |
| `GET` | `/healthz` | Internal/proxy | Chat UI process liveness |
| `GET` | `/readyz` | Internal/proxy | DB/auth/proxy readiness without leaking config |

**Non-public endpoints:** no generic `POST /hermes/proxy/{path}` or full URL forwarder. Expose a **fixed explicit allowlist** of supported Hermes operations only.

### 9.1 Error response envelope

```json
{
  "error": {
    "code": "ACCOUNT_PENDING",
    "message": "Account is waiting for administrator approval",
    "requestId": "req_..."
  }
}
```

Codes include: `UNAUTHENTICATED` (401), `ACCOUNT_PENDING` (403), `ACCOUNT_REJECTED` (403), `ACCOUNT_SUSPENDED` (403), `TENANT_NOT_READY` (409/503), `FORBIDDEN` (403), `NOT_FOUND` (404 for foreign resources), `CAPABILITY_UNAVAILABLE` (409/501), `RATE_LIMITED` (429), `HERMES_UNAVAILABLE` (503), `RUN_CONFLICT` (409), `UPSTREAM_TIMEOUT` (504), `BUDGET_REACHED` (403, the user has used the token limit they opted into, summed across all their chats via `PUT /api/me/budget`; new messages are refused until the limit is raised or removed, a running reply finishes). Never echo bearer tokens, raw upstream URLs, or secret diagnostics.

---

## 9A. Hermes published API inventory → Chat UI mapping (v2.1 verified inventory retained in v2.3, required)

> **Interpretation:** This is the HTTP/SSE API surface described in the Hermes API Server documentation and the published API-server source, **not** a claim that all routes are enabled in the user's already-deployed build. Actual Hermes image/version, enabled routes, configured credentials, toolsets and memory provider are **not accessible/verified from this PRD authoring environment**. Codex must run the deployment preflight in §9A.8 before implementing against a route. Do not confuse the Hermes Web Dashboard API, TUI/ACP JSON-RPC, or third-party provider APIs with the HTTP API Server contract.
>
> **Exposure:** "Published by Hermes" means available to authenticated server-side callers inside the private Docker network. **Never publish Hermes to the browser or public edge.** All browser calls terminate on Chat UI Go API, which validates Google session, admin approval and resource ownership before using a per-user profile-scoped Hermes credential.

### 9A.1 Source precedence and verification grades

| Grade | Meaning | Engineering treatment |
|---|---|---|
| **DOC** | Explicitly documented in official Hermes API Server guide | Can design adapter; still probe deployed capability/version |
| **SRC** | Present in published `gateway/platforms/api_server.py` capability/route declaration, but less completely documented | Add integration test for path, schema and security before enabling UI |
| **OPTIONAL** | Authenticated documented route but disabled by default, capability-flagged, profile-configuration-dependent or subject to additional security controls | Show only if live capabilities and tests affirm support |
| **NOT API** | Hermes tool action / provider capability, not a general-purpose HTTP route | Invoke by an agent turn only; don't invent REST endpoints |
| **APP** | Endpoint implemented in Chat UI, not in Hermes | Define user-facing contract ourselves, own authorization and data shape |

Primary docs: https://hermes-agent.nousresearch.com/docs/user-guide/features/api-server ; source contract: https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/api_server.py ; integration guide: https://hermes-agent.nousresearch.com/docs/developer-guide/programmatic-integration . These were inspected 21 October 2026; **deployed version must be probed independently**.

**Upstream URL resolution:**

```text
Browser  -> https://chat.example.com/api/... (cookie / app UUID)
Chat UI  -> http://hermes:<internal-port>/p/<SERVER_RESOLVED_PROFILE>/<hermes-path>
           Authorization: Bearer <SERVER_RESOLVED_PROFILE_KEY>
           (when multiplex profile routing is enabled)
        OR http://hermes:<per-profile-port>/<hermes-path>
           Authorization: Bearer <PROFILE_KEY>
           (when dedicated-port routing is enabled)
```

The `p/<profile>` prefix and per-profile port are **mutually exclusive examples of deployment modes**; neither is user-controllable. A shared API Server key is not a multi-tenant security boundary. All per-user session/run IDs are resolved from Chat UI DB ownership mappings; never concatenate a browser-provided Hermes ID into upstream paths.

### 9A.2 Sessions, historical turns, conversation management

| Hermes method + path | Verified | Chat UI API (app-owned) | UI component / action | Required adapter behavior |
|---|---|---|---|---|
| `GET /api/sessions?limit=&offset=&source=&include_children=` | DOC | `GET /api/chats` | Sidebar conversation list / pagination | Filter **server-side owner index** first; Hermes returns profile sessions, not Google-user identity. Do not expose foreign/default sessions. |
| `POST /api/sessions` | DOC | `POST /api/chats` | New chat | Create a session only on the caller's mapped profile; persist mapping and reconcile failures. |
| `GET /api/sessions/{id}` | DOC | `GET /api/chats/{chatId}` | Chat header / metadata | Resolve owned upstream session; verify profile binding and status. |
| `PATCH /api/sessions/{id}` | DOC | `PATCH /api/chats/{chatId}` | Rename conversation | Map allowed title/end-reason fields, update app index consistently. |
| `DELETE /api/sessions/{id}` | DOC | `DELETE /api/chats/{chatId}` | Delete conversation | Track upstream deletion, app mapping cleanup and assets; no false success on partial failure. |
| `GET /api/sessions/{id}/messages` | DOC | `GET /api/chats/{chatId}/messages` | **Complete historical chat turns** | Request `include_compacted=true` for archived turns and `inline_images=false` for compact payloads when supported; preserve order, role, timestamps/IDs where available. **Native message cursor pagination is not documented**. |
| `POST /api/sessions/{id}/fork` | DOC | `POST /api/chats/{chatId}/fork` (P1) | Branch conversation action | Create **new app chat mapping** to forked upstream session under same owner; never transfer fork to another user. |
| `POST /api/sessions/{id}/model` | SRC | `PUT /api/chats/{chatId}/model` (P1) | Persistent session model lock | Capability `session_model_lock`; validate body against actual build, enforce allowlisted model/provider and show lock precedence. |
| `POST /api/sessions/{id}/chat` | DOC | `POST /api/chats/{chatId}/messages` fallback only | Chat composer | Synchronous single turn; do not block all browser requests; preserve terminal `status`. |
| `POST /api/sessions/{id}/chat/stream` | DOC | `GET /api/runs/{runId}/events` via adapter fallback | Streamed text, commentary, tools | This is upstream POST returning SSE; normalize events; test reconnect semantics and session-key compatibility before selecting over Runs API. |

**Historical-turn fidelity requirement `HIST-001` (P0):** if Hermes has archived turns due to context compaction, the Chat UI should recover those turns using the documented `include_compacted=true` option where available; absence, truncation or unavailable media must be represented explicitly rather than silently treating the active LLM context as the complete chat record. `inline_images=false` is a payload optimization, **not** an image-download endpoint. For an owned historical image, resolve the referenced owned asset using an approved mechanism; otherwise display a safe placeholder.

**History pagination contract `HIST-002` (P0):** Chat UI `GET /api/chats/{chatId}/messages?cursor=<opaque>&limit=50` is an **APP contract**, not a Hermes-native cursor. Define stable ordering `(Hermes turn identity or monotonic index, app chat ID)`; prove zero duplication and stable `nextCursor` on reload. If fetching full Hermes history is required, use bounded resources and explicitly document upper limits; indexed server-side snapshots/caches may be needed to satisfy long histories without repeated full downloads. If an upstream message ID is not stable, use an app-owned ordered snapshot identity and reconcile changes. Never persist another user's transcript in a shared unscoped cache.

**History lazy-load rule `HIST-003` (P0):** The default chat-open request MUST NOT send all historical turns to the browser. Return a bounded newest page (default `limit=50`, configurable ceiling), `nextCursor`, and a stable ordered turn identity. Load preceding pages only upon user scroll/request, preserve viewport anchor, and avoid N+1 upstream reads. If Hermes only exposes full transcripts, build/use a bounded user-owned pagination adapter or index; enforce upstream fetch size/time limits and a visible degraded-state/error when those limits are exceeded. This is not permission to indiscriminately mirror entire transcripts into SQLite.

### 9A.3 Primary agent run, tool stream and human control

| Hermes method + path | Verified | Chat UI API | UI component | Request / response / security notes |
|---|---|---|---|---|
| `POST /v1/runs` | DOC | `POST /api/chats/{chatId}/messages` | Send prompt | Body: `input`, owned `session_id`, allowed `model`, `provider`, `model_options` (as supported). Headers: `Authorization`, app-generated `Idempotency-Key`, stable per-user `X-Hermes-Session-Key`. Returns `run_id` and initial status; record admission transaction. |
| `GET /v1/runs/{run_id}` | DOC | `GET /api/runs/{runId}` | Running / finished / usage status | Returns real terminal state, output and optional usage/runtime; also `pending_controls` for certain modes. Poll after SSE break / restart. |
| `GET /v1/runs/{run_id}/events` | DOC | `GET /api/runs/{runId}/events` | Token/tool/commentary/progress events | Upstream SSE → authorized app SSE with bounded event journal and reconnect reconciliation. Preserve `run.completed`, `run.failed`, `run.cancelled`, `run.interrupted` distinctions. |
| `POST /v1/runs/{run_id}/stop` | DOC | `POST /api/runs/{runId}/cancel` | Stop response | Returns `stopping` and is not immediately terminal; poll until settled. |
| `POST /v1/runs/{run_id}/approval` | DOC | `POST /api/runs/{runId}/approve` | Tool permission card | Submit exact upstream `request_id`/`execution_generation`/`choice` where required; validate current pending control and choices; stale/foreign prompts fail closed. |
| `POST /v1/runs/{run_id}/clarify` | DOC | `POST /api/runs/{runId}/clarify` (P1) | Agent clarification form | Only if advertised/verified, owner-bound response to exact pending clarification. |
| `POST /v1/runs/{run_id}/steer` | DOC/SRC | `POST /api/runs/{runId}/steer` (P1) | Mid-run guidance | Guard on advertised feature, active run owner, exact schema and supported tool boundary; no arbitrary cross-run steering. |
| `POST /v1/runs/{run_id}/resolve-unknown` | DOC/OPTIONAL | `POST /api/runs/{runId}/resolve-unknown` (internal or restricted UX) | Restart recovery banner | Only when capability `run_unknown_resolution` is true and run status is `unknown`; exact `admission_id` + `execution_generation` from status; **do not automatically replay unknown execution**. |

**Run lifecycle `RUN-001`:** poll/stream status alone does not guarantee persistent historical content; once terminal, reconcile session transcript for canonical turns. `status=unknown` is **not** completed or failed; request explicit recovery flow and warn the user. Durable `Idempotency-Key` semantics differ by execution mode and endpoint; verify deployed configuration. No auto-approve, auto-resolve, or auto-retry of potentially side-effecting tools.

**Wire shape examples** (adapted from documented fields; tests must assert actual deployed JSON):

```http
POST /p/<profile>/v1/runs HTTP/1.1
Authorization: Bearer <profile-secret>
Content-Type: application/json
Idempotency-Key: <server-generated-unique-key>
X-Hermes-Session-Key: chatui:user:<immutable-user-uuid>

{"session_id":"<owned-hermes-session>","input":"Summarize my notes","model":"<allowed-model>","provider":"<allowed-provider>"}
```

```json
{"run_id":"run_abc123","status":"started"}
```

```http
POST /p/<profile>/v1/runs/run_abc123/approval HTTP/1.1
Authorization: Bearer <profile-secret>
Content-Type: application/json

{"request_id":"<prompt_id>","execution_generation":3,"choice":"once"}
```

`run_abc123` is illustrative, not an observed live ID. Never log secrets or echo upstream IDs to browsers unless replaced with app-owned IDs. Tool output is untrusted content; OpenUI actions remain backend-authorized.

### 9A.4 Chat/Responses compatibility, models, capabilities and monitoring

| Hermes method + path | Verified | Chat UI mapping | UI / use case | Notes |
|---|---|---|---|---|
| `POST /v1/chat/completions` | DOC | Optional compatibility adapter, not default | Legacy OpenAI-compatible client | `messages` array, `stream`; stateless history pattern. Hermes SSE may include `hermes.tool.progress`; not preferred for durable chat history. |
| `POST /v1/responses` | DOC | Optional adapter (P2) | Stateful Responses clients | `input`, `previous_response_id` or named `conversation`, streaming structured `function_call` items; do not mix response chain with app session without tested mapping. |
| `GET /v1/responses/{id}` | DOC | Internal adapter only (P2) | Retrieve prior Responses API result | Not a substitute for complete session historical turns. |
| `DELETE /v1/responses/{id}` | DOC | Internal adapter only (P2) | Retire Responses chain item | Not a general conversation deletion endpoint. |
| `GET /v1/models` | DOC | `GET /api/models` fallback | Simple model alias | Returns agent/profile alias, **not** full selectable model/provider catalog. |
| `GET /api/model/options` (`?refresh=1` optional) | DOC | `GET /api/models` | Model picker and capability/cost hints | Preferred for UI; sanitize credentials/provider-private metadata; refresh rate-limited and only by authorized clients. |
| `GET /v1/capabilities` | DOC | `GET /api/capabilities` / internal adapter | Dynamic feature gating | Enumerates features + endpoints; **authoritative deployed discovery source**, not an auth/tenant isolation test. |
| `GET /v1/skills` | DOC | `GET /api/skills` (P1) | Skills browser (read-only) | Capability `skills_api`, profile-scoped read-only metadata; no arbitrary install/config API presumed. |
| `GET /v1/toolsets` | DOC | `GET /api/toolsets` (P1) | Available tools panel | Shows enabled/configured toolsets and actual tool names; tool listing does not mean UI can call each tool by its own HTTP endpoint. |
| `GET /health` | DOC | Backend readiness probe only | Admin system status | Cheap liveness, not evidence agent can execute / profile healthy. |
| `GET /v1/health` | DOC | Backend compatibility probe only | Internal monitoring | Alias for liveness. |
| `GET /health/detailed` | DOC | `GET /api/admin/system` sanitized (deferred, see §3.4) | Admin readiness details | Bearer protected; returned HTTP 200 can still mean degraded; inspect `status` and `readiness.checks`. |

**Model override `MODEL-001`:** `model` + `provider` + request-scoped `model_options` may be sent to `/v1/runs` and session chat endpoints. Session model lock and route aliases can take precedence; client must reflect **effective** model from actual run `runtime`, not assume the picker was honored. Bare `model` for Chat Completions/Responses may be ignored unless `direct_model_requests=true`, while Hermes-native `/v1/runs` honors model selection subject to policy/config. Never let the UI submit arbitrary provider URL, secret or command.

**Reasoning visibility `MODEL-002`:** render only reasoning/commentary that Hermes explicitly emits as public stream content and policy permits; do not misrepresent it as complete private chain-of-thought. `assistant.commentary` frames must remain distinct from finalized assistant answer; `already_streamed` should prevent duplicates.

### 9A.5 Scheduled jobs (conditional, P1)

| Hermes method + path | Verified | Chat UI API | UI |
|---|---|---|---|
| `GET /api/jobs` | DOC | `GET /api/jobs` | My scheduled tasks |
| `POST /api/jobs` | DOC | `POST /api/jobs` | Create task (validated schedule/prompt/allowed delivery) |
| `GET /api/jobs/{job_id}` | DOC | `GET /api/jobs/{jobId}` | Job details/run state |
| `PATCH /api/jobs/{job_id}` | DOC | `PATCH /api/jobs/{jobId}` | Edit schedule/prompt |
| `DELETE /api/jobs/{job_id}` | DOC | `DELETE /api/jobs/{jobId}` | Delete task (may cancel in-flight run) |
| `POST /api/jobs/{job_id}/pause` | DOC | `POST /api/jobs/{jobId}/pause` | Pause |
| `POST /api/jobs/{job_id}/resume` | DOC | `POST /api/jobs/{jobId}/resume` | Resume |
| `POST /api/jobs/{job_id}/run` | DOC | `POST /api/jobs/{jobId}/run` | Run now |

**Job guard `JOB-001`:** Hermes jobs list is profile-scoped, not a Google-user ACL. Chat UI stores app-owned job mapping and validates every operation. Restrict delivery targets so jobs cannot send private outputs or assets to another user's channel/email/webhook. Suspension must disable future scheduling or safely pause affected jobs; if no verified revocation handling, **hide jobs for P0**.

### 9A.6 Browser-control, artifacts, and feature-specific surfaces — never automatically expose

| Hermes method + path | Verified | Chat UI exposure | Notes |
|---|---|---|---|
| `POST /v1/browser-control/register` | DOC/OPTIONAL | **No v1 public mapping** | Browser-extension controller registration; requires enabled browser extension control and a scoped ticket, not a chat-browser automation shortcut. |
| `GET /v1/browser-control/ws` | DOC/OPTIONAL | **No v1 public mapping** | WebSocket subprotocol/ticket flow for the browser controller; not normal chat SSE. |
| `POST /v1/artifacts/upload` | SRC/OPTIONAL | **No generic media mapping** | Source advertises scoped browser-control artifacts. Validate its actual body, controller authorization and MIME limits before choosing it for any supported use. |
| `GET /v1/artifacts/download/{artifact_id}` | SRC/OPTIONAL | **No generic media mapping** | Requires explicit artifact scope/authorization, not an unauthenticated public attachment URL. |
| Memory writes / provider setup / global config mutation | NOT API | **Never implement as generic proxy** | Current published static capability flags include `memory_write_api: false` and `admin_config_rw: false`; do not fabricate these endpoints. |

**Media contracts `MEDIA-001`:** Hermes can *use* web search, image/video generation, speech/TTS and related tools through its agent runtime when configured. Their existence does **not** imply a standard published `POST /v1/images/generations`, `/v1/videos`, `/v1/audio/speech`, `/v1/audio/transcriptions`, or downloadable asset route. The published source currently marks `audio_api: false` and `realtime_voice: false`; those values are **not** proof that a TTS agent tool is unavailable, only that native audio/realtime HTTP endpoints are not advertised. Implement UI media features as agent prompts + streamed tool lifecycle + returned artifacts, after integration fixtures verify result format, private storage and media URL ownership. No new provider-facing API should be called directly from Chat UI.

**Asset lifecycle `MEDIA-002`:** show image/audio/video previews only from URLs the backend has verified and can serve through owned `GET /api/assets/{assetId}`; download from private provider URLs via an explicit server-side allowlist and size/MIME limits (avoid SSRF). For asynchronous video output track run/job status and artifact delivery independently; never infer completed generation from a tool-start event.

**OpenUI `UI-001`:** OpenUI Lang is not a Hermes native API endpoint. The agent emits text/structured markup in a run; Chat UI safely validates and renders it. No `POST /v1/openui` or direct Hermes OpenUI parser is assumed. All OpenUI follow-up actions are app API actions re-authorized server-side; a component cannot request privileged API methods or bypass approvals.

### 9A.7 Browser-facing Go API additions / corrections to §9

The following are **APP endpoints** to add to the Chat UI contract in §9 (not endpoints exposed by Hermes to browsers):

| Go method + path | Phase | Maps to | Why / guard |
|---|---|---|---|
| `GET /api/chats/{chatId}/messages?cursor=&limit=` | P0 | `GET /api/sessions/{id}/messages?include_compacted=true&inline_images=false` | Deterministic historical-turn pagination; backend owns cursor/index implementation. |
| `GET /api/models` | P1 | `GET /api/model/options` → fallback `GET /v1/models` | Sanitized allowed provider/model catalog for model picker. |
| `GET /api/skills` | P1 | `GET /v1/skills` | Profile-scoped read-only skill list. |
| `GET /api/toolsets` | P1 | `GET /v1/toolsets` | Profile-scoped tool availability, not direct execution. |
| `PUT /api/chats/{chatId}/model` | P1 | `POST /api/sessions/{id}/model` | Validated per-session model lock, only if live capability exists. |
| `POST /api/chats/{chatId}/fork` | P1 | `POST /api/sessions/{id}/fork` | Private branch and new owned app chat ID. |
| `POST /api/runs/{runId}/steer` | P1 | `POST /v1/runs/{id}/steer` | Only on active owned run; capability-gated. |
| `POST /api/runs/{runId}/resolve-unknown` | P1 / restricted | `POST /v1/runs/{id}/resolve-unknown` | Explicit unknown-state recovery. |
| `GET /api/jobs/{jobId}` | P1 | `GET /api/jobs/{id}` | User-owned job details. |
| `POST /api/jobs/{jobId}/pause` | P1 | `POST /api/jobs/{id}/pause` | Pause owned job. |
| `POST /api/jobs/{jobId}/resume` | P1 | `POST /api/jobs/{id}/resume` | Resume owned job. |
| `POST /api/jobs/{jobId}/run` | P1 | `POST /api/jobs/{id}/run` | Manual run of owned job. |

Some original §9 endpoints are intentional **app abstractions**, not 1:1 Hermes mappings: Google OIDC/login/approval/admin/audit have **no Hermes analogue**; `POST /api/assets` is an app private asset staging flow until a safe relevant Hermes integration is proven; `GET /api/admin/system` (deferred) would be an aggregate of app health, per-profile Hermes readiness and sanitized telemetry. Revocation/reapproval and workspace provisioning remain Chat UI / trusted operator responsibilities.

### 9A.8 Mandatory deployed-Hermes API preflight and acceptance evidence

Run these requests **from the Chat UI container/private network** using a **dedicated, non-production test profile**. Never put tokens in shell history, logs, URLs, repo files or screenshots. Do **not** probe a real user's data. Record sanitized method/status/schema/capabilities and Hermes image digest/commit only.

```bash
# Example only; host, routing mode, credentials must come from runtime secrets.
# Source variables from a protected secret mechanism (do not paste real keys).
: "${HERMES_PRIVATE_BASE_URL:?}"
: "${HERMES_PROFILE_KEY:?}"

curl --fail-with-body --silent \
  -H "Authorization: Bearer ${HERMES_PROFILE_KEY}" \
  "${HERMES_PRIVATE_BASE_URL}/v1/capabilities"

curl --fail-with-body --silent \
  -H "Authorization: Bearer ${HERMES_PROFILE_KEY}" \
  "${HERMES_PRIVATE_BASE_URL}/api/model/options"

curl --fail-with-body --silent \
  -H "Authorization: Bearer ${HERMES_PROFILE_KEY}" \
  "${HERMES_PRIVATE_BASE_URL}/api/sessions?limit=1&offset=0"

curl --fail-with-body --silent \
  -H "Authorization: Bearer ${HERMES_PROFILE_KEY}" \
  "${HERMES_PRIVATE_BASE_URL}/health/detailed"
```

The implementation must generate a **machine-readable inventory fixture** (`docs/hermes-api-capabilities.example.json`, sanitized), a verified mapping table (`docs/hermes-api-mapping.md` or this section), and CI-compatible adapter tests using captured **redacted** responses. At minimum:

1. Record Hermes image digest/tag, active profile routing mode, unique profile credentials, API listener and reachable private network. Confirm an unauthorized user cannot access Hermes via public edge or direct host port.
2. Capture `GET /v1/capabilities`: feature flags and `endpoints` objects; compare against §9A inventory; missing or changed routes are marked **unsupported in deployed build** rather than assumed. Capability exposure alone is not proof of tool isolation.
3. Create a test session, send two turns, retrieve original historical turns, trigger/fixture an archived/compacted transcript, fetch `include_compacted=true`, and compare canonical order/content. Assert image placeholders and owned asset fetch behavior. Test large history + pagination fallback.
4. Start a run with unique `Idempotency-Key`, poll and stream, handle `: keepalive`, disconnect/reconnect, cancel, failed/unknown paths, and verify no duplicate assistant turns. Never replay potentially side-effecting runs by default.
5. Verify approval/clarify/steer/resolve-unknown only when advertised; test stale prompt and generation refusal. Ensure all controls deny a suspended user immediately.
6. Query available models, skills and toolsets. Probe tool output formats (web result sources, generated image/video/audio) **only if those toolsets are configured**, through a test agent run. Verify no direct `/v1/images`/`/v1/audio`/`/v1/videos` assumptions.
7. Test user A profile key against B profile prefix/port; test A's session/run/asset/job IDs through B's Go API login; all must fail closed. Attempt filesystem traversal/tool reads of sibling profile; **do not declare multi-user safe** if global tools defeat filesystem sandbox.
8. Test Chat UI database restart and Hermes restart independently: all old chat metadata/turns remain accessible only to their original user, and failed reconnections do not duplicate turns.
9. Never auto-run jobs or OpenUI actions at preflight; if jobs are enabled, test their creation/pause/revocation with a disposable job and a benign delivery target.
10. Gate release: **P0 UI surfaces only routes verified in the deployed build**, with feature-level reason codes; unverified feature buttons are hidden or disabled. Security proof is separate from endpoint availability.

### 9A.9 Explicit ownership matrix / no-Hermes-route items

| Desired product behavior | Real owner | Hermes endpoint? |
|---|---|---|
| Google Login / OIDC `sub` | Chat UI / Google | **No** |
| Admin approve, reject, suspend, account audit | Chat UI SQLite + Go RBAC | **No** |
| Account-to-profile binding, secret reference, isolation verification | Chat UI + trusted Hermes operator | **No public safe auto-provisioning endpoint assumed** |
| One user, many chats, history and compacted turns | Hermes sessions + Chat UI owner index | **Yes**, Sessions API |
| User-private long-term memory | Hermes profile and configured memory provider | **Agent-internal**, no generic memory write REST API presumed |
| Web search / image gen / video gen / TTS | Hermes agent tools + configured cloud provider | **Via agent run**, not automatically standalone HTTP endpoints |
| OpenUI UI generation | Hermes text/structured output + Chat UI renderer | **No native OpenUI API** |
| Historical OpenUI replay/renderer version | Chat UI app metadata or validated Hermes transcript | **No dedicated OpenUI replay API** |
| Database backup, per-user data deletion compliance and admin recovery | App + Hermes operator | Mixed; not safely solved by one Hermes request |

---

## 10. Database and storage (Chat UI only)

### 10.1 SQLite schema outline

Use SQLite `WAL`, prepared statements, foreign keys, transactional migrations, safe busy timeout, DB file on persistent volume, restrictive file permissions. **Single Chat UI write instance** is assumed; do not run many replicas on one SQLite file over unsupported shared filesystem.

```sql
-- Identity is stable across email/name changes.
CREATE TABLE users (
  id TEXT PRIMARY KEY,
  google_issuer TEXT NOT NULL,
  google_sub TEXT NOT NULL,
  email TEXT NOT NULL,
  display_name TEXT,
  avatar_url TEXT,
  role TEXT NOT NULL CHECK (role IN ('USER','ADMIN')),
  approval_status TEXT NOT NULL CHECK
      (approval_status IN ('PENDING','APPROVED','REJECTED','SUSPENDED')),
  tenant_status TEXT NOT NULL CHECK
      (tenant_status IN ('UNPROVISIONED','CONFIGURING','READY','FAILED','DISABLED')),
  created_at TEXT NOT NULL,
  approved_at TEXT,
  approved_by TEXT,
  last_login_at TEXT,
  UNIQUE(google_issuer, google_sub)
);

CREATE TABLE hermes_bindings (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL UNIQUE REFERENCES users(id),
  profile_name TEXT NOT NULL UNIQUE,
  routing_mode TEXT NOT NULL CHECK (routing_mode IN ('MUX_PREFIX','DEDICATED_PORT')),
  upstream_endpoint_id TEXT NOT NULL,
  credential_ref TEXT NOT NULL UNIQUE,
  memory_scope_key TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL CHECK (status IN ('CONFIGURING','READY','FAILED','DISABLED')),
  verified_at TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  token_hash TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  revoked_at TEXT
);

CREATE TABLE chats (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  hermes_binding_id TEXT NOT NULL REFERENCES hermes_bindings(id),
  hermes_session_id TEXT NOT NULL,
  title TEXT,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(hermes_binding_id, hermes_session_id)
);

CREATE TABLE runs (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  chat_id TEXT NOT NULL REFERENCES chats(id),
  hermes_run_id TEXT,
  idempotency_key TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE assets (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  chat_id TEXT REFERENCES chats(id),
  storage_path TEXT NOT NULL,
  media_type TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE job_bindings (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  hermes_binding_id TEXT NOT NULL REFERENCES hermes_bindings(id),
  hermes_job_id TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(hermes_binding_id, hermes_job_id)
);

CREATE TABLE approval_audit (
  id TEXT PRIMARY KEY,
  target_user_id TEXT NOT NULL REFERENCES users(id),
  actor_user_id TEXT NOT NULL REFERENCES users(id),
  action TEXT NOT NULL,
  from_status TEXT,
  to_status TEXT,
  reason TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE security_audit (
  id TEXT PRIMARY KEY,
  actor_user_id TEXT,
  action TEXT NOT NULL,
  target_kind TEXT,
  target_id TEXT,
  outcome TEXT NOT NULL,
  request_id TEXT,
  created_at TEXT NOT NULL
);
```

The above is a **conceptual executable starting schema**, not complete DDL. Add indexes, explicit session/device metadata, status check constraints for runs, credential rotation details, admin action idempotency, and migrations as needed. For authorization, always query with `WHERE id = ? AND user_id = ?`, and verify the linked profile binding belongs to that same user. Do not rely on frontend-filtered lists.

### 10.2 Canonical data placement

| Data | Primary store |
|---|---|
| Google user profile + approval | Chat UI SQLite |
| App sessions + role + policy | Chat UI SQLite |
| Mapping chat/run/job → user/profile | Chat UI SQLite |
| Hermes transcripts + memory + model/tool state | Existing Hermes profile storage |
| Generated media metadata/ownership | Chat UI SQLite |
| Private media file bytes | Chat UI owned private volume if ingested; otherwise owned Hermes media references proxied after validation |
| Admin/security audit events | Chat UI SQLite + sanitized structured logs |

Do not replicate entire Hermes transcripts into the Chat UI database by default. Any caching for OpenUI rendering or transcript search must be scoped by user and retention policy.

### 10.3 Deletion and retention

- Deleting a chat requests Hermes session deletion and purges associated app mappings and private asset references; failures are recorded as pending cleanup, **not** reported as complete.
- User access removal and user data erasure are separate actions.
- Backups of Chat UI SQLite/assets and Hermes profiles are independently encrypted and subject to retention. Cross-profile recovery cannot restore A into B.
- User account deletion includes revoking sessions, stopping/pausing runs/jobs, deleting app-owned data and an operator-backed Hermes profile purge if needed. Show incomplete status if upstream erasure is not verified.

---

## 11. Files, media, and content security

- Current Hermes API documents **inline images** in specific chat endpoints but does **not** document generic file-upload support for PDFs/DOCX/files through those APIs. Do not promise arbitrary document attachments in P0.
- Image inputs: validate image MIME by content sniffing, size, dimensions, magic bytes, and owning chat; route via supported Hermes multimodal schema.
- Generated image/video/audio: Hermes performs provider/tool call; Chat UI receives a returned file/URL/reference, validates type/host, and ingests/serves through a user-owned route whenever possible.
- Long-running video generation: represent as Hermes run/job, not as a synchronous Chat UI HTTP request with an indefinite timeout.
- **Never** accept arbitrary URL fetching from model output without egress allowlist and SSRF protections. Block private IP ranges, DNS rebinding, Unix/file schemes, redirects to internal endpoints.
- Per-user storage quotas, file-size limits, download rate limits, antivirus/content-safety checks as required for deployed use, safe Content-Disposition and CSP.
- Pending/suspended/rejected accounts cannot use historical signed links if access is revoked; favor authenticated streaming or very short-lived signed URLs with revocation strategy.

---

## 12. Jobs, scheduling and background execution (P1)

Hermes exposes jobs endpoints in documented versions. Scheduled jobs create **new execution paths** outside user chat requests, so they must be user-scoped and governed by approval status.

Requirements:

1. Every job is created under the user's dedicated Hermes profile with app-owned mapping.
2. Jobs for pending/rejected users can never be created or enumerated.
3. Suspending a user must also **pause/cancel their jobs upstream** and verify reconciliation. If upstream is unreachable, mark `SUSPENSION_PENDING_JOB_SHUTDOWN` and show an admin security alert.
4. Job results (including delivered media) belong to the originating user; no global delivery channel by default.
5. Add rate limits, maximum job count, allowed schedules and maximum runtime.
6. Expose job UI only after end-to-end owner isolation and suspend behavior are tested. Otherwise keep Jobs feature hidden.

---

## 13. Security, abuse prevention, and observability

### 13.1 Security controls (P0)

- OAuth verifier + secure app sessions, full RBAC, tenant binding, object ownership and protected SSE.
- Every user ID is taken from app session; never trust a client-supplied Google sub, profile, tenant, or credential alias.
- Admin action replay protection, CSRF defense, strict `Origin` and secure cookie handling.
- Deny CORS to Hermes browser-facing origin. Hermes API listens only on private internal interface/network or is otherwise blocked from Internet access.
- Secrets via Docker secrets/environment injection or protected Chat UI secret store; never in JS bundle, logs, URLs, traces, or metrics labels.
- Tool descriptions and results are untrusted; use structured renderers, sanitize Markdown and asset URLs.
- Content security policy compatible with static React and approved media providers, no wildcard `script-src`.
- No raw user prompts/tool arguments/tool outputs in default operational logs. Audit **metadata**, not private content.
- Admin telemetry is aggregated and scoped; system dashboard must never display someone else's prompt, memory or message content.
- Rate limits by user plus global cap: login, chat sends, runs, approvals, admin changes, uploads, asset downloads, OpenUI actions.
- Handle resource exhaustion: request size, stream event size, in-memory queue, attachment size, timeouts, concurrency.

### 13.2 Observability

Record non-sensitive fields: `request_id`, `app_user_id` (internal opaque), `app_chat_id`, `app_run_id`, profile binding identifier (not key), route, response status, latency, bytes, error category, upstream capability version, run terminal state, token usage if provided.

Metrics: pending registrations, approvals/rejections, active accounts, Hermes tenant readiness, chat/run success/failure/cancel, upstream latency, SSE disconnect/reconnect, denial counts, asset storage, tool approvals, job reconciliation failures.

`/healthz` is cheap liveness; `/readyz` confirms Chat UI DB and minimum dependencies without exposing tokens; admin health view additionally reads user-profile readiness when authorized.

### 13.3 Production threat test suite

- IDOR/BOLA: chat IDs, run IDs, approvals, asset IDs, jobs, transcript endpoints.
- Google OIDC: replayed state/nonce, wrong `aud`, wrong `iss`, missing `email_verified`, expired token, forged email.
- Privilege escalation: bootstrap/admin grant, client-edited role/status, stale session, concurrent approval.
- Streaming: cross-user event replay, reconnect after suspension, shared event source, duplicate submit.
- Profile routing: missing profile, wrong profile key, malformed prefix, test using default key on named profile, profile mismatch.
- Memory/tool leakage: shared scope, shell reads sibling profile, traversal, symlink, external memory namespace, prompt injection in web content.
- OpenUI: raw script/HTML, arbitrary queries/mutations, overlarge nested UI, foreign user action targets.
- SSRF: generated media URL targets internal Docker hostname, cloud metadata endpoint, redirect to loopback.
- Jobs: user suspended while job runs, job ownership guessing, failure to pause a job.

**Release blocker:** Any reproducible cross-user content/memory/tool exposure, unauthorized tool execution, or bypass of pending status.

---

## 14. Non-functional requirements, performance budgets, and suggested SLOs

The following are **engineering targets to validate in load tests**, not measured Hermes performance claims.

| Area | Proposed target / contract |
|---|---|
| Containers | Exactly one new Chat UI application container; do not redeploy existing Hermes as part of this PR |
| UI deployment | Go API + built React assets from one container; existing reverse proxy |
| Storage | Embedded SQLite WAL + mounted persistent app volume |
| Availability | Graceful Chat UI error if Hermes is unavailable; no bypass of authorization |
| Streaming | Initial local app event quickly after run admission; no buffering entire Hermes response |
| Concurrency | Per-user and global bounded simultaneous runs; configurable defaults |
| OIDC | Durable identity across email/display-name changes |
| Isolation | Zero successful cross-user negative tests |
| Security | Zero pending/suspended requests reaching Hermes |
| Admin status | State changes visible on next authenticated API request; active SSE actively terminated best effort |
| Accessibility | Keyboard navigation, labelled inputs, usable at mobile widths |
| Backup | App DB + assets recoverable; Hermes profile backup separately documented |
| Upgrades | Version pin Hermes adapter compatibility and OpenUI library; upstream downgrade safe |
| Browser rendering | Turn pagination + virtualization; defer heavyweight OpenUI and non-visible media |
| Resource budget | Target Chat UI container working-set RAM ≤256 MiB for the explicitly defined light-load scenario (§14.2); not a universal guarantee |

### 14.1 Low-footprint implementation contract (required)

The application MUST preserve the **single new Chat UI container** design. Hermes remains the source of LLM inference/tool work; its container memory and end-user browser memory are **excluded** from the Chat UI RAM budget. Do not optimize by bypassing per-user authorization or by returning incomplete history as if complete.

- **`PERF-001` — Lazy Loading & Rendering (P0 for chat/history; P1 for media/OpenUI):**
  1. **Turns:** Fetch only the newest, cursor-paginated page when opening a chat (default page size 50, server-enforced maximum). Fetch older pages on upward scroll with request deduplication; retain a stable scroll anchor when pages are prepended. No full-history download to the browser on chat open, even when Hermes returns all messages to the adapter.
  2. **Long chat rendering:** Use virtualized/windowed message rendering above a measured threshold (configurable, initially 100 rendered turns), including OpenUI/Markdown/tool blocks. Avoid keeping hundreds of mounted React trees; preserve keyboard focus, accessibility, copy/select operations, and scroll-to-latest behavior.
  3. **Media:** Render lightweight placeholders/thumbnails for images, video and audio. Fetch original user-owned assets only when visible or explicitly opened/downloaded; never preload full video/audio files. Serve thumbnails and originals through authenticated, ownership-checked app URLs, with MIME/size validation and cancellation on navigation.
  4. **OpenUI:** Code-split heavyweight registries (charts, data grids, media players, rich forms) using dynamic imports; load only component types present in the visible message. Progressive/incomplete streaming markup remains a lightweight safe placeholder until a valid renderable unit is ready. Cache compiled component modules, not unbounded per-chat component trees.
  5. **Search/sidebar:** Paginate recent conversations and search results; no automatic fetch of every conversation or message on login. Explicit empty/loading/error/retry states and no background fan-out across all user sessions.
  6. **Correctness:** Older historical turns, including compacted Hermes transcript turns, MUST remain retrievable and consistently ordered. Lazy loading reduces transfer/rendering, not data retention or user authorization. Every page and media request rechecks ownership.

- **`PERF-002` — Static frontend / backend packaging (P0):** Build React/TypeScript assets at image-build time; Go serves compressed static assets and APIs. No Node.js/Next.js server process in production. Reuse the existing edge reverse proxy; do not add a Chat UI worker or extra container solely for frontend rendering.
- **`PERF-003` — Bounded Go streaming (P0):** Flush/relay Hermes SSE incrementally; no reading an entire agent response into memory. Set bounded per-connection buffers, upstream and downstream cancellation, heartbeat and timeout, maximum event size, per-user/global stream quotas, and backpressure policy. Disconnect/reconnect follows §§6.3 and 9A.3 idempotency/recovery rules, without duplicate turns.
- **`PERF-004` — Embedded persistence (P0):** SQLite WAL on a persistent volume, bounded connection pool/busy timeout, indexed user/chat ownership lookups, and limited metadata caching. Do not add Redis or PostgreSQL to satisfy ordinary chat history, login, and approvals in v1.
- **`PERF-005` — Canonical transcript ownership (P0):** Hermes remains the canonical chat transcript source. Chat UI stores auth, ownership, session/run mappings, status, asset metadata, and any strictly necessary bounded per-user page indices. Full duplicate copies of every transcript are **not** the default. If an indexed snapshot is needed for stable cursor pagination, document its size ceiling, user scope, invalidation/reconciliation, encryption/retention and deletion semantics; prevent any cross-user cache key collisions.

### 14.2 Performance benchmark and acceptance targets

**`PERF-006` — Measured resource envelope (P0 before production):** The numbers below are engineering **targets**, not verified usage of a deployed Chat UI. Test on the intended edge host (or hardware with documented equivalence) with Hermes running separately. Measure only the `chatui` container using Docker/cgroup metrics and separately record browser performance.

| Scenario | Test workload | Required observation / target |
|---|---|---|
| Idle | Warm app, no active streams; observe ≥10 minutes | Record container working set, CPU and process RSS; no unexplained sustained growth |
| Light load | 3 approved signed-in users, up to 2 simultaneous chat SSE streams, normal text/tool-event activity for ≥30 minutes | **Target:** container working-set RAM P95 ≤256 MiB, no OOM/restarts, responsive streaming; report peak as well |
| Long history | One owned conversation with ≥1,000 historical turns, including compacted-turn fixtures where supported | Initial UI receives only newest page (default ≤50); scrolling fetches stable cursor pages without duplicates or missing turns; virtualized DOM count bounded |
| Rich content | Chat with mixed charts/tables, images, video/audio placeholders and an OpenUI action | Only visible/used renderer chunks and media transfer; all assets ownership checked; no eager full-media download |
| Reconnect/cleanup | Abrupt client disconnect/reconnect, cancellation, chat switch, and 5 repeated cycles | No permanent per-run buffers, abandoned subscriptions, duplicated assistant turns, or sustained memory growth |

**Measurement contract:** Specify image digest, Go version, browser build, Hermes version, memory definition (`container memory.current`/working-set method and RSS), sample period, traffic fixture and concurrent-user count. Report idle, P50/P95/peak RAM, CPU and stream errors. The 256 MiB number is a **light-load target**; if unmet, profile and optimize first, then document why a higher safe limit (e.g. 512 MiB) is required instead of hiding OOMs or changing tests. Do not automatically set a production `mem_limit: 256m` solely because the benchmark target exists; leave headroom based on measured peak.

**Release gate:** `PERF-001` turn pagination and `PERF-002`–`PERF-005` are P0 implementation requirements; media/OpenUI lazy chunks can land with corresponding P1 features. `PERF-006` requires recorded benchmark evidence and zero OOM. Exceeding the 256 MiB target is an explicit performance exception requiring owner review, **not** permission to compromise security, durability, or turn-history completeness.

---

## 15. Docker and configuration contract

### 15.1 Network and exposure

- `chatui` and `hermes` must share a Docker bridge network or an equivalently authenticated private reachability path; **being on the same physical host alone is insufficient** if container network reachability is missing.
- Reuse an existing edge reverse proxy for TLS and `https://<chat-domain> → chatui:<port>`.
- Hermes port and dashboard are **not** exposed publicly for Chat UI users. If currently public, treat this as deployment hardening dependency.
- The private DNS alias `hermes` is **illustrative**; discover the actual container/service name.

### 15.2 Illustrative Chat UI service definition

This **does not** redeploy Hermes; it only connects Chat UI to an existing externally managed Docker network.

```yaml
services:
  chatui:
    image: ghcr.io/your-org/hermes-chatui:<pinned-version>
    restart: unless-stopped
    environment:
      APP_PUBLIC_URL: https://chat.example.com
      HTTP_ADDR: 0.0.0.0:8080
      DATABASE_PATH: /data/chatui.db
      ASSET_ROOT: /data/assets
      GOOGLE_OIDC_CLIENT_ID: ${GOOGLE_OIDC_CLIENT_ID}
      GOOGLE_OIDC_CLIENT_SECRET: ${GOOGLE_OIDC_CLIENT_SECRET}
      SESSION_SIGNING_KEY: ${SESSION_SIGNING_KEY}
      HERMES_ENDPOINT_ALLOWLIST: http://hermes:8642
      HERMES_ROUTING_MODE: multiplex
      # Profile API keys are server-side secret references, never client config.
    volumes:
      - chatui_data:/data
    networks:
      - edge_private
    expose:
      - "8080"
    security_opt:
      - no-new-privileges:true
    read_only: true
    tmpfs:
      - /tmp:rw,nosuid,nodev,size=64m

volumes:
  chatui_data:

networks:
  edge_private:
    external: true
    name: ${EXISTING_PRIVATE_NETWORK}
```

**Caveats:** `ghcr.io/your-org/...`, hostname, values and network above are placeholders, not resources verified on your server. Supply credentials via trusted deployment secrets instead of committing a real `.env`. `HERMES_ROUTING_MODE=multiplex` is only valid after verifying the deployed Hermes gateway actually enables/supports it. If not, configure dedicated port mappings and an allowlist of those internal endpoints.

### 15.3 Application configuration

- Google client ID/secret, callback URL, allowed origin, signed session key, cookie policy.
- Database path, asset root, upload limits, max request sizes.
- Hermes endpoint allowlist, verified routing mode, client timeouts, capability refresh interval.
- Credential reference provider (file/env/secret store), per-profile mapping, rotation.
- Maximum concurrent runs, message size, SSE heartbeat, media quotas, job enablement.
- Admin bootstrap CLI (one-time), audit/retention settings, relevant feature flags.

---

## 16. Delivery phases and Definition of Done

### Phase 0 — Integration/security proof (hard gate)

*Recorded results and fixtures: [`docs/hermes/PHASE0-FINDINGS.md`](hermes/PHASE0-FINDINGS.md).*

**Deliverables:** exact Hermes image/version, network connectivity proof, API capability snapshot, verified multi-profile routing, per-profile key tests, profile-vs-tool isolation assessment, test A/B users, admin bootstrap method.

**DoD:** Two unique profile bindings work in the existing container; user A cannot read B's sessions and memory through allowed API/tools; admin auth/status matrix passes. If not, **block** multi-user chat rather than proceed with shared profile.

### Phase 1 — Minimum secure Chat UI (P0)

- Go HTTP server serves React static bundle; SQLite migrations/storage, bounded connections and SSE proxy (`PERF-002`–`PERF-004`).
- Cursor-based lazy historical turn loading, stable scroll position and long-thread virtualization (`HIST-003`, `PERF-001` P0); do not eagerly hydrate whole conversations.
- Google OIDC login/logout and pending/rejected/setup/suspended screens.
- Admin bootstrap, account review, approval/rejection/suspension, audit.
- Secure Hermes profile binding and tenant verification workflow.
- Owner-scoped chats, history, streaming run lifecycle, error/retry/cancel/reconnect.
- Private memory scoping and cross-user negative tests.
- Basic Markdown UI plus OpenUI renderer integration foundation and safe fallback.
- Docker image deployed next to existing Hermes; Caddy route and private networking.

**DoD:** A pending account cannot create any Hermes workload, approved-but-unprovisioned cannot chat, active approved A/B can chat privately, revoked users lose access, UI works on desktop/mobile, no public Hermes credentials.

### Phase 2 — Complete Hermes chat capabilities and mature UX parity (P1)

- Implement all `UXP-001..UXP-012`, `UXP-014` and `UXP-015` product-parity requirements in §21; `UXP-013` (admin-controlled extras) may be gated as specified; defer only individual upstream-unsupported actions with a documented capability/proof blocker, not a silent omission.
- PWA install/offline shell, per-device owner-bound Web Push, in-chat and cross-chat full-text search, per-chat drafts, prompt slash templates, rich Markdown composer, context/cost/quota, voice UX, HTML/SVG safe preview, Markdown/PDF export, scroll/keyboard polish.
- Turn rewind/regen/edit, mid-run steer, and disconnect recovery must use verified Hermes APIs, audited actor/session ownership and explicit non-support states. Do not import Aixell filesystem hacks.

- Dynamic model/settings/toolset/skills discovery, tool progress.
- Image input and generated image/audio/video previews where supported.
- OpenUI component registry: charts, data tables, cards, interactive input, safe actions.
- Lazy import heavyweight OpenUI renderer modules; lazy thumbnails and on-demand owner-checked media transfer (`PERF-001` P1).
- Run approvals/clarification, admin capability status, quotas/usage.
- Private media asset handling; jobs UI only after suspension/job tests pass.

**DoD:** Every UI-shown capability is backed by real Hermes provider/tool config; OpenUI action security suite passes; no invented LLM/tool behavior. All §25 feature-parity E2E tests pass or are classified as upstream-blocked with recorded API evidence and agreed release exception. The final app must not ship as a basic-chat-only MVP presented as complete.

### Phase 3 — Hardening and quality

- Complete the cross-user threat tests for search indices, saved drafts/templates, push subscriptions, exports, voice uploads, preview sandbox, rewind/steer and delayed notifications (§25).
- Measure foreground and background browser/PWA behavior, service-worker cache privacy and long-chat virtualization; profile Go CPU/RAM with rich-content/notification workloads.

- Fuzz property tests for ownership and SSE recovery.
- Rate limits/quotas, backup restore, database corruption recovery, error analytics.
- Admin account lifecycle UX polish and audit export (metadata only).
- Run §14.2 reproducible performance benchmark, including ≤256 MiB light-load RAM target, long-thread pagination, lazy media/OpenUI and no-OOM checks; document any exception.
- Additional accessibility, cross-browser/mobile E2E checks and update strategy.

**DoD:** No P0 security issue open, deployment/restore walkthrough demonstrated, measured resource envelope recorded, upgrade smoke tests pass.

Recorded resource envelope (idle, concurrent chat load at N=10/50/100, database size, per-SSE-stream cost, disk growth; measured on a scratch instance with a fabricated Hermes): [`docs/runbooks/capacity.md`](runbooks/capacity.md).

---

## 17. Acceptance matrix / QA checklist

| ID | Scenario | Expected |
|---|---|---|
| `ACC-001` | First Google login | User created `PENDING`; no Hermes calls |
| `ACC-002` | Pending user sends API request directly | `403`, no Hermes request |
| `ACC-003` | Admin approves account without binding | `APPROVED + UNPROVISIONED`; blocked Chat |
| `ACC-004` | Tenant verification succeeds | `READY`; user can chat |
| `ACC-005` | Admin rejects/revokes | Chat/history/assets/stream/actions blocked as defined |
| `ACC-006` | User edits own role or status in payload | No privilege change |
| `TEN-001` | Users A/B use independent profiles | Distinct keys, stores, sessions, scopes |
| `TEN-002` | A saves memory; B asks for it | B gets no A memory |
| `TEN-003` | B requests A chat/run/asset ID | `404/403`; no content |
| `TEN-004` | B attempts filesystem/profile traversal via tools | Denied by verified boundary or unsafe tool disabled |
| `TEN-005` | Wrong default key on named Hermes profile | Upstream request rejected |
| `CHAT-001` | Chat reload/continue | Same owned Hermes session |
| `CHAT-002` | Duplicate submit after network failure | Single Hermes run through idempotency |
| `CHAT-003` | Hermes run fails after assistant text | UI shows failed, not success |
| `CHAT-004` | Browser disconnects/reconnects | Correct owner-only recovery |
| `CHAT-005` | Two users stream concurrently | No crossed tokens/events |
| `PERF-ACC-001` | Open a 1,000-turn chat | Only newest ≤50 turns fetched initially; ordered cursor scroll retrieves all authorized turns, including compacted fixtures |
| `PERF-ACC-002` | Scroll through a very long thread | Bounded mounted message nodes; viewport/focus preserved; no visible duplicate/missing turns |
| `PERF-ACC-003` | Open chat containing image, video, audio and charts | Non-visible media does not download in full; heavyweight OpenUI modules loaded on demand |
| `PERF-ACC-004` | Chat UI idle/light-load/reconnect benchmark | Record P50/P95/peak metrics; light-load RAM P95 target ≤256 MiB; no OOM, leaks or unauthorized cache sharing |
| `PERF-ACC-005` | Inspect production image/processes and data stores | Go serves static React, no runtime Node.js, SQLite WAL, no Redis/Postgres, no unconditional full transcript duplication |
| `UI-001` | Valid OpenUI Lang | Renders approved interactive React components |
| `UI-002` | Invalid OpenUI | Graceful safe fallback |
| `UI-003` | Malicious OpenUI action | Backend refuses unauthorized mutation |
| `MEDIA-001` | Generated private asset | Accessible only to owning active user |
| `MEDIA-002` | Hermes unsupported file input | UI disables rather than silently pretending success |
| `OPS-001` | Hermes outage | Graceful user-visible error; no permission bypass |
| `OPS-002` | Suspended account has scheduled job | Job paused/cancelled or explicit admin alert/blocker |
| `OPS-003` | Chat UI redeploy | Account statuses/session mappings remain durable |
| `DEP-001` | Deployment inspection | Only Chat UI container added; Hermes unchanged |

---

## 18. Engineering work breakdown (for Codex)

### 18.1 Backend packages (suggested)

```text
cmd/chatui/
internal/config/
internal/httpserver/
internal/auth/googleoidc/
internal/auth/session/
internal/authorization/
internal/admin/
internal/storage/sqlite/
internal/hermes/client/
internal/hermes/capabilities/
internal/hermes/tenantrouter/
internal/hermes/events/
internal/chat/
internal/runs/
internal/assets/
internal/audit/
internal/jobs/                # P1, feature-gated
web/                          # React/TypeScript, built into Go static assets
migrations/
tests/integration/
tests/security/
docs/
```

### 18.2 Suggested implementation order

1. Enumerate deployed Hermes capabilities **without changing Hermes deployment**; document version/profiles/routes, reproduce with a temporary test binding.
2. Implement DB schema and server-side authorization with tests **before** chat UX.
3. Google sign-in + stable identity, no implicit chat access.
4. Admin bootstrap + pending/approve/reject/suspend + full audit.
5. Profile binding and verified tenant router; reject any unknown profile.
6. Chat CRUD + run event proxy + idempotent reconnect + cancel.
7. Per-user memory scope + cross-tenant security tests.
8. React chat UX + lazy turn pagination/virtualization + OpenUI renderer/code splitting + safe action gateway (`PERF-001`).
9. Tool/media/skills/models/jobs capabilities progressively, driven by real Hermes discovery.
10. Docker packaging, rollout, §14.2 repeatable RAM/CPU benchmark and performance report, backup/restore, security review.

### 18.3 Codex implementation rules

- Read this PRD and perform a **Phase 0 integration spike first**. Do not guess Hermes endpoint semantics; record real responses/fixtures from the deployed version (redacting keys).
- Do not fork Hermes, write a second LLM framework, deploy another database, or bring in an extra agent runtime.
- Do not add Docker socket access to Chat UI or make `chatui` modify Hermes data directories.
- Implement server-owned routing; no user-supplied profile/API key/header/URL/session ownership.
- Every DB query touching user resources must enforce ownership; run negative IDOR tests with test users A/B.
- Maintain a capability matrix and `unsupported` states; no fabricated media success or model options.
- Use typed upstream HTTP clients, structured errors, SSE event normalization, transaction boundaries, and explicit timeout/cancellation handling.
- Use OpenUI only for rendering; all subsequent actions route via secured Go API with a registered allowlist.
- Keep admin blinded to private conversations by default.
- Changes must include unit tests, integration fixtures, E2E flows, migrations, security tests, and updated deployment docs.
- **Never claim secure multi-user production readiness until tool filesystem isolation test passes**, or restrict toolset to those that cannot cross the boundary.

---

## 19. Dependencies, risks, and open verification items

| Risk/dependency | Required decision before release | Owner |
|---|---|---|
| Existing Hermes version unknown | Pin actual image digest and API capabilities; verify profile-key behavior | Operator + implementer |
| Same edge network reachability unverified | Confirm `chatui → hermes:<port>` on private network, DNS alias and edge Caddy route | Operator |
| Profiles not configured | Provision user-specific profile/key, map via admin config, verify health | Hermes operator |
| Global Hermes tool access | Disable unsafe tools or prove true filesystem/OS sandbox; otherwise block arbitrary-user access | Security/operator |
| Memory backend uses different scoping model | Confirm active provider, memory path and stable session-key semantics | Hermes operator |
| Provider-dependent tools | Verify deployed toolset/provider configured (web search, image, video, TTS) | Hermes operator |
| OpenUI with Hermes model | Verify prompt injection/render reliability and valid streaming format | Chat UI implementer |
| Video job retention/media sizes | Enforce async handling, ownership, caps and storage cleanup | Chat UI implementer |
| Jobs continue on user revocation | Pause/stop supported jobs or hide jobs until safe | Chat UI implementer |
| Admin recovery | Out-of-band bootstrap and break-glass process; no auto-first-admin | Operator |

**Release decision:** App can be built and used with a single trusted user while Phase 0 multi-user prerequisites are unresolved, but **cannot be represented as secure for multiple mutually untrusted users** until those boundaries are demonstrated.

---

## 20. Primary documentation references (verified 21 October 2026; updated in §9A)

- Hermes API Server — endpoints, profile-prefixed auth, capability discovery, runs, sessions, memory scope: https://hermes-agent.nousresearch.com/docs/user-guide/features/api-server
- Hermes API Server source and published capability endpoint inventory: https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/api_server.py
- Hermes Programmatic Integration — API Server vs TUI/ACP protocols: https://hermes-agent.nousresearch.com/docs/developer-guide/programmatic-integration
- Hermes Profiles — per-profile state and filesystem caveats: https://hermes-agent.nousresearch.com/docs/user-guide/profiles/
- Hermes Docker — existing container profile supervision and port mapping: https://hermes-agent.nousresearch.com/docs/user-guide/docker
- Hermes Session Storage — state DB and profile home: https://github.com/NousResearch/hermes-agent/blob/main/website/docs/developer-guide/session-storage.md
- Hermes Security — tool/container boundaries: https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/security.md
- Google OpenID Connect reference (`sub`, `email_verified`, `iss`, `aud`): https://developers.google.com/identity/openid-connect/reference
- OpenUI Lang overview: https://github.com/thesysdev/openui/blob/main/docs/content/docs/openui-lang/index.mdx
- OpenUI Renderer and actions: https://www.openui.com/docs/openui-lang/renderer
- OpenUI architecture/query/mutation model: https://www.openui.com/docs/openui-lang/architecture
- OpenUI React UI package/peer dependency documentation: https://github.com/thesysdev/openui/blob/main/packages/react-ui/README.md

**Document boundary:** Anything requiring privileged changes inside Hermes (profiles, keys, mounts, tool execution sandbox, gateway config) is an **external prerequisite/integration dependency** of this Chat UI project. This document specifies the interface and release gates; it does **not** authorize Chat UI to change the existing Hermes container by itself.

---

## 21. Mature Chat UX / Aixell reference parity — normative implementation requirements (v2.3)

**Purpose.** This section converts observed interaction patterns from the user-provided `aixell-main.zip` into testable requirements for **our** Chat UI. These requirements add to (never replace) §§1–20: one logical assistant, per-user Hermes profiles and memory, Google OIDC, admin approval, strict data isolation, Go/React/SQLite, two separate containers on one edge, SSE, OpenUI, and measured small footprint.

**Reference limitations.** The ZIP source demonstrates code paths but was not run against the target Hermes container. Aixell is a *personal/single-operator app*; its authentication/profile layout and direct access to `~/.hermes` or local Python scripts are **not** acceptable patterns for our multi-user deployment. Repo access as a contributor does not establish permission to republish or copy proprietary code; use the feature behavior and public interfaces as a design reference, or obtain explicit license/permission before reuse. We are **not** introducing Node.js or Python into Chat UI runtime.

### 21.1 Feature contract and release priority

| ID | User-visible capability | User outcome and required implementation | Priority / gate |
|---|---|---|---|
| `UXP-001` | Installable PWA | App installs on supported mobile/desktop browsers; manifest, correct icons, service worker, responsive layout, update prompt; offline app shell **only**, no offline access to cached private chat by default | P1 (mature UX release) |
| `UXP-002` | Push on agent completion | User may opt in per browser/device; receive a generic completion/failure alert after a run finishes even if app is backgrounded; click deep-links to **owned** chat after auth; no prompt or private content in notification payload | P1; opt-in and browser support |
| `UXP-003` | Full-text chat search | Search **own conversation titles and own historical message bodies**, with filters and highlights; results include owning chat and turn anchor, archived/compacted turns where indexable; no other user's matches | P1; index must be privacy-safe |
| `UXP-004` | Voice input / dictation | Mic button: start/stop recording, playback and discard before send; speech-to-text as a capability-gated workflow through Hermes or a documented approved *Hermes-side* adapter. Browser-native OS dictation may be offered separately with explicit privacy notice | P1; no invented Hermes STT endpoint |
| `UXP-005` | Markdown composer | Comfortable multiline editor: fenced code blocks, toolbar or shortcuts (bold/italic/code/list), Tab inside code, send/newline keys, paste/drop image, attachments; no raw HTML execution | P1 |
| `UXP-006` | Slash prompt templates | User CRUD for personal reusable `/commands`, autocomplete, keyboard selection, editable expansion; optionally admin-distributed read-only defaults; never execute a slash template as a server command | P1 |
| `UXP-007` | Draft per conversation | Save unsent composer state independently by user and chat, including edit position/markdown mode; restore across reload/devices when server sync enabled; no leaking drafts to other users or profiles | P1 |
| `UXP-008` | Context usage meter | Display token usage from reported Hermes data; input/output/cache/reasoning when available; proportion of known context window; mark estimates vs confirmed metrics | P1; never invent context window |
| `UXP-009` | Cost/quota/budget | Own usage per chat/day/model, estimated cost with price provenance/freshness, no fabricated actual cost; soft/hard user limits administered server-side where enforceable; clear unavailable states | P1; actual provider quota only if supported |
| `UXP-010` | Turn editing / regenerate / rewind | Retry assistant response, edit prior user turn, optionally fork from turn; preserve safety, ownership and historical truth. **No direct Hermes DB editing, script execution or `/undo` via shell from Chat UI** | P1 capability-gated; see §21.5 |
| `UXP-011` | Steer while running | Send a user-owned in-flight guidance message when native Hermes steer is supported; show pending/accepted/rejected state and explicit failure if unsupported | P1, verified upstream `/v1/runs/{id}/steer` |
| `UXP-012` | Resilient browser reconnect | Reload, tab switch, disconnect or Chat UI restart does not produce duplicate runs or show a false completed state; resume events where possible then reconcile canonical transcript | P0 core, polish P1 |
| `UXP-013` | HTML/SVG artifact preview | Show untrusted generated HTML/SVG in strongly sandboxed preview (or safe static thumbnail); no access to app credentials, parent origin, private API, or network; allow copy/download raw artifact with warnings | P1, gated by hardened sandbox test |
| `UXP-014` | Export Markdown / PDF | Export own chat with selected range/timestamps, user & assistant turns, source links and textual placeholders for tools/media; Markdown client-generated; PDF through browser print/export without heavyweight server render process | P1 |
| `UXP-015` | Mature day-to-day chat ergonomics | Responsive collapsible/resizable sidebar, mobile focus handling, scroll-to-bottom, pinned/renamed chats, keyboard shortcuts, copy code and links, progressive tool cards, errors, theme, per-chat model prefs, light/dark if supported | P1 |
| `UXP-016` | PWA share target (optional) | On supported browsers receive shared text/files from OS into **a draft**, never auto-send or cross into wrong account/chat | P2 / optional, owner/privacy tests |

**Scope rule.** P1 means **required for the planned full-feature release**, not permission to leave the product incomplete. Ship Phase 1 as an explicitly labeled secure beta if necessary; do not describe it as feature parity. If Hermes lacks a needed API, present a named `BLOCKED_BY_UPSTREAM` gap with a deploy-version probe result and decide whether a narrowly scoped Hermes-side extension or changed requirement is acceptable. Do not fake behavior, mutate Hermes SQLite directly, or invoke LLM provider APIs from Chat UI.

### 21.2 PWA, Web Push, and signed-in devices

- PWA installs from the **Chat UI origin**, with Web App Manifest icons/shortcuts and service worker versioning; service worker scope excludes direct Hermes origin, admin endpoints and authenticated API caches.
- Cache **hashed public JS/CSS/icons and an anonymous offline shell**; `Cache-Control: no-store` for sessions, messages, drafts, user settings, asset blobs and account-status responses. On logout/account suspension, instruct service worker to purge user-bound state; **server authorization remains the real boundary**.
- Push subscription is bound to `{user_id, device_id, endpoint_hash, browser metadata, created_at, revoked_at}`. VAPID private key stays in backend secret storage. Validate HTTPS push endpoint destinations against a maintained safe allowlist / DNS & IP checks; never act as arbitrary HTTP callback service.
- Notification preference settings: all completions / failures only / muted; permission requested **only after a user gesture**. Do not nag after denial. No silent default subscription.
- Push send occurs only from a confirmed Hermes terminal status and only if user is still `APPROVED + READY`. Deduplicate per `{user, run, event, device}`. Browser push payload should contain no prompts, tool output, memory, snippets, titles that may be sensitive, secrets, or transcript text. On click, open `/chat/<app_chat_id>` and re-authenticate if needed.
- Push best-effort semantics (not guaranteed delivery); transient retry bounded; revoked/suspended/deleted users lose subscriptions and notifications. On Hermes outage/unknown run, do not report success.
- Existing Caddy terminates HTTPS; add **no** cloud messaging proxy worker/container. A library may be used within Go; no separate Node push service.

### 21.3 Search across **all of a user's own historical chat turns**

- Search UI includes a global field (sidebar/palette) and scoped "find in this chat". Results are paginated, show chat title, short snippet, timestamp, and **stable turn anchor**, with match highlighting without executing HTML.
- Search must include historical user/assistant text as well as archived/compacted turns where Hermes returns them; exclude raw tool secrets and untrusted system-only internals. A result must never expose a foreign user's chat ID, filename, snippet, or existence.
- Source of truth = Hermes historical transcript (§9A.2). Choose, in order: (A) a verified upstream search API; (B) a **derived, per-user bounded search index** in Chat UI SQLite FTS5, populated via authorized incremental history sync. This derived index is an **explicit scoped exception** to the no-full-transcript-copy default (§10.2 / `PERF-005`); it is not canonical transcript storage. Store only necessary normalized searchable text/excerpts with retention controls; indexes keyed by `user_id + chat_id + turn_identity`, never global unscoped data.
- FTS5 is **not encrypted by SQLite by default**. Restrict DB file permissions, persistent volume access, backups and export; if the threat model requires at-rest encryption, choose a verified encrypted storage solution rather than claiming FTS5 is encrypted.
- Incremental indexing is bounded (rate/batch size, startup sync, per-user quota); no scanning every profile/chat on each login. Handle transcript compaction, deletions, regenerate/rewind and fork by invalidation/upsert. Retention/deletion must clear base rows, FTS postings, temporary caches and queued indexing tasks.
- Deep-link result → owner-check chat → lazy-fetch page containing turn → scroll and highlight; virtualization must support anchors for unloaded turns. Distinguish "indexing" and "no results"; do not silently claim a partial index is complete.

### 21.4 Voice: recording, transcription, playback, and TTS boundaries

- Provide explicit capture button, record timer, mic permission/error states, file size/duration ceiling, cancel/discard/preview. Browser handles capture via MediaRecorder; compressed audio is uploaded through owned `/api/assets` with MIME/size checks. **No automatic upload before user approval**.
- **Two workflows are distinct**: (1) dictation (audio → editable text in composer) and (2) attaching audio to a Hermes chat when supported. Audio playback/TTS generated by Hermes follows §9A.6 and owned media rules.
- Hermes currently has documented agent tools but **a generic HTTP transcription endpoint is not guaranteed**. Phase 0/2 must probe deployed Hermes speech capabilities. If no safe upstream route exists, mark dictation `BLOCKED_BY_UPSTREAM` and offer OS/browser-native dictation only behind an explicit UX/privacy decision: some browsers may use platform/vendor cloud processing. It must not be silently portrayed as Hermes-native/private processing.
- No Chat UI direct `OpenAI /v1/audio/transcriptions`, ElevenLabs or other provider endpoints; no shell `ffmpeg` required in Chat UI production unless justified and memory-benchmarked. If normalization is needed, prefer browser conversion or a sandboxed Hermes-side capability; ensure recording duration and audio size caps.
- Voice transcripts are treated as user drafts until explicitly sent; upload, transcribed text, and TTS asset IDs are user-owned and follow delete/revocation policy.

### 21.5 Rewind, edit, retry, and steer semantics (important correctness distinction)

**Never conflate:** `retry run`, `edit last user message`, `fork conversation from a turn`, `delete last turns`, and `steer active run`. These have different side effects and required upstream guarantees.

- **Regenerate assistant answer:** if Hermes supports a documented safe replay/turn-level mutation route, verify method, historical identity and idempotency; otherwise **fork from known session/turn if actually supported** or create a new user-owned chat with explicit "continuation may differ" semantics. A blind second `POST /v1/runs` into the same session is *not* a reliable regenerate operation.
- **Edit a prior user turn:** default to non-destructive **branch** preserving original conversation and creating an owned new session. Exact prefix/turn-level fork is not documented solely by the existence of `POST /api/sessions/{id}/fork`; must probe turn-fork support. If not possible, offer explicit copy prompt into new chat, not fabricated context preservation.
- **Rewind/delete last turn:** only with verified native Hermes API that does not break transcript/memory consistency. Do not emulate by editing Hermes `state.db`/transcript or launching Hermes Python scripts from Chat UI.
- **Steer live run:** use `POST /v1/runs/{run_id}/steer` only if capability advertised and run active, with backend-resolved owner and upstream generation; display accepted/failed status and do not append an invented final chat turn.
- Any destructive operation demands user confirmation, no pending active conflicting run, updated history/search index, cache invalidation, audit event (metadata only) and immediate check that run/session is still owned/approved. Preview before irreversible removal. On upstream inability, feature is clearly disabled with a reason, not silently hidden in a misleading "retry" action.

### 21.6 Markdown editor, templates, drafts and turn display

- Composer supports Markdown shortcuts, inline fenced code, indentation, paste files/images, drag-and-drop, chip previews, precise Enter/Shift+Enter behavior and mobile IME composition safety. Prefer small editor or headless text editing primitives; do **not** add an entire heavyweight rich-text engine without a benchmark and necessity proof.
- Save each draft by `{user_id, chat_id}`. Also support new-chat draft with separate sentinel per user. Debounce writes; conflict resolution across tabs/devices by `updated_at`/revision, don't overwrite newer server drafts. Drafts can be encrypted at rest only if genuinely implemented and tested; never claim encryption by default.
- Slash template menu filters personal templates when user types `/`; templates are text insertions, not Hermes skills/tool calls. CRUD validates bounded name/body; unique per owner, safe rendering, optional admin defaults read-only and explicitly labeled. Protect against injection by storing text only; user can edit before sending.
- Group one user prompt + assistant outputs + tool activity into **logical turns** with stable IDs, chronological ordering, source provenance and explicit incomplete/cancelled states; show compact tool cards and expandable details only for owning user. Maintain fidelity of historical turns and assistant Markdown/OpenUI rendering after refresh.
- Controls: copy message/code, regenerate when supported, quote prompt, timestamps, native browser selection, scroll anchor preservation while streaming, keyboard shortcuts with opt-out, resizable sidebars at desktop, accessible mobile gestures/drawers, `prefers-reduced-motion`, theme choice; don't enable broad full-history preloads.

### 21.7 Usage, context, pricing and budgets

- **Source precedence:** Hermes run/session usage → verified provider metadata surfaced by Hermes → admin-maintained/versioned pricing catalog. Never claim an **estimated USD cost** is an actual settled provider charge; show price timestamp/source if used.
- Per-chat meter: input/output/cache/reasoning tokens when supplied, context window and usage proportion **only if both numerator and denominator are valid for the same metric**. Context `input_tokens` may include tools/system memory and vary by provider; label carefully. Unknown → `—` / "Unavailable".
- Per-user dashboard: 7/30/90 day totals, by chat and model where obtainable, chart view with accessible data table, filters. Data is user-owned metadata; admin sees aggregate counts/cost policies only, not conversation content or raw provider secrets.
- Budgets: opt-in **soft warning** at user-defined limit; **hard admission control** only for known, enforceable limits before starting a run. Actual provider spend after in-flight run can exceed admission estimate. No guarantee of exact dollar ceilings without a true provider reservation/billing API. Rate-limit run creation and concurrent media jobs; user cannot bypass org-level caps by changing client settings.
- Quota/status is read-only provider information when Hermes actually exposes it; no unaudited provider APIs or token handling in frontend or Chat UI. Provide clear staleness/inapplicability badges in the product, not invented numerical balances.

### 21.8 HTML/SVG preview, artifact downloads and exports

- Text-generated HTML/SVG must be treated as hostile. Prefer sanitized static preview or a **dedicated sandboxed origin with isolated origin** and strict CSP. `iframe sandbox` must **not** combine `allow-scripts` and `allow-same-origin` for same-origin untrusted content; no `allow-top-navigation`, forms, popups, credentials or network requests. If robust preview isolation can't be demonstrated, show source/download only.
- Renderer must never evaluate model JavaScript in the Chat UI origin; forbid arbitrary OpenUI component imports/fetches. SVG must be sanitized or rasterized; do not inject raw SVG via `innerHTML` into trusted DOM.
- Export Markdown contains only owned historical turns and selected metadata, with optional omission of tool details and private source links. Implement export incrementally for long chats; show progress and stop on ownership loss.
- PDF should favor browser-native print-to-PDF for simplicity, without a server-side Chromium/Playwright renderer in `chatui`. Add print-only CSS (page breaks, code wrapping, tables, image captions) and warn when video/audio cannot be embedded. Server-side PDF/HTML conversion is **not P1 required** and requires new footprint/security review.
- For exported media references, embed only user-owned URLs resolved with current authorization and explicit user action. Expired/private URLs require download or placeholder. Exporting a chat never grants another account access to its assets.

### 21.9 Reference implementation observations (not a code-copy authorization)

| Aixell ZIP source inspected | Observed behavior to emulate | Required architectural change in our app |
|---|---|---|
| `public/sw.js`, `public/manifest.webmanifest`, `lib/push.js` | Installable PWA, per-device push handling | Google-auth owner binding, revocation/expiry purge, Go VAPID implementation, private payloads |
| `public/lib/find-in-chat.js`, `public/lib/sessions.js`, `lib/state-db.js` | Search/scroll-to-match and stored chat state | Scoped indexed message search + Hermes transcript reconciliation and compaction |
| `public/lib/recorder.js`, `lib/transcribe.js` | Dictation/recording UX + transcription | No direct provider or local `ffmpeg` unless separately approved; Hermes capability gateway |
| `public/lib/md-editor.js`, `public/lib/composer.js`, `lib/templates.js`, `public/lib/drafts.js` | Rich composer, slash templates, drafts | Per-user, per-chat records + CSRF/ownership; React rendering |
| `public/lib/usage.js`, `lib/usage.js`, `lib/pricing.js`, `lib/budget.js` | Token/price/context/budget UI | Hermes-derived usage and verified prices; no false billing/quota guarantees |
| `public/lib/redo.js`, `lib/rewind.js`, `lib/rewind.py`, `public/lib/approval.js` | Edit/regenerate/rewind and approvals | **Do not copy** Python/DB direct access; native Hermes API and explicit feature gate only |
| `public/lib/export.js`, `public/lib/artifact.js`, `public/lib/openui.js` | Markdown/PDF export, rich previews and OpenUI | Print-friendly React export, safe sandbox, ownership and action authorization |
| `public/lib/sse.js`, `lib/runs.js`, `public/lib/queue.js` | Streaming and reconnection ergonomics | Server-owned session/run mapping, idempotency, SSE checkpoint and no cross-user subscription |

These references are pointers in the **user-supplied ZIP**, not API guarantees for Hermes and not a statement that all Aixell behaviors have been load- or security-tested.

---

## 22. Mature UX ↔ Chat UI API ↔ Hermes ownership mapping

All endpoints below are **new Chat UI Go application endpoints**. They are not claims that Hermes publishes the same paths. Use §9A for actual published Hermes inventory and deployed-version preflight.

| UX feature | Go API (authenticated unless noted) | Backend storage / Hermes mapping | Privilege/security |
|---|---|---|---|
| PWA shell | `GET /manifest.webmanifest`, `GET /sw.js` | Static Go-served hashed assets | Static public files only; service worker forbidden from caching private responses |
| Push setup | `GET /api/push/public-key`, `POST /api/push/subscriptions`, `DELETE /api/push/subscriptions/{id}`, `GET /api/me/notifications` | Per-device app subscription; Go VAPID worker; Hermes run terminal event only | Approved+ready owner; no content in payload; CSRF for mutation |
| Push preference | `PUT /api/me/notifications` | App user preference | Owner-only; admin suspension overrides |
| Chat search | `GET /api/search/chats?q=&cursor=&limit=`, `GET /api/chats/{id}/search?q=&cursor=` | Verified native Hermes search **or** owned SQLite FTS derived index | Owner filter included *inside* search query, not client filter |
| Drafts | `GET/PUT/DELETE /api/chats/{id}/draft`; `GET/PUT/DELETE /api/me/new-chat-draft` | App SQLite `drafts` | Owner+revision/ETag to resolve multi-device concurrent edits |
| Templates | `GET/POST /api/me/templates`, `PUT/DELETE /api/me/templates/{id}` | App SQLite user templates | Text only; admin defaults optionally separate read-only route |
| Voice upload | `POST /api/assets` (audio kind), `POST /api/assets/{id}/transcribe` | Owned media staging; Hermes-side STT **only if documented and verified** | Capability-gated; no direct provider in Chat UI; rate/size/duration caps |
| Context / usage | `GET /api/chats/{id}/usage`, `GET /api/me/usage?range=`, `GET /api/me/budget`, `PUT /api/me/budget` | Hermes session/run usage; sanitized price catalog; app budget prefs | Owner-only, price provenance, unknown ≠ 0 |
| Regenerate / edit / rewind | `POST /api/chats/{id}/regenerate`, `POST /api/chats/{id}/edit-turn`, `POST /api/chats/{id}/rewind` | **Only** verified Hermes turn mutation/fork semantic; never raw DB or Python | Owner + pending-run guard + clear destructive confirmation + capability flag |
| Live steer | Existing `POST /api/runs/{runId}/steer` (§9A.7) | Verified `POST /v1/runs/{id}/steer` | Owner+generation check, no arbitrary rerouting |
| Run reconnect | Existing `GET /api/runs/{runId}`, `GET /api/runs/{runId}/events` | Hermes `GET /v1/runs/{id}`, `/events`, canonical session re-read | Owner+status on each resume, idempotency replay protection |
| Artifacts | `GET /api/assets/{id}`, `GET /api/chats/{id}/artifacts` | Owned app assets or Hermes tool returned artifact references | Private MIME allowlist / owner check / media range requests |
| Export | `GET /api/chats/{id}/export-data?format=markdown&cursor=` | Owner-only Hermes history iterator with compaction | Streaming bounded JSON/text chunks; PDF via browser print |

**Additional Hermes probe tasks**: capture the actual deployed API's support (or absence) for transcript `include_compacted`, run steer, session fork granularity, safe rewind, voice input/STT, terminal run recovery, usage aggregates and tool/media artifact references. Persist a capability snapshot for UI flags. No speculative `/v1/audio/transcriptions` or `/api/undo` route allowed.

**Endpoint availability policy:** Always return structured `FEATURE_UNSUPPORTED`, `UPSTREAM_UNAVAILABLE`, `USER_NOT_APPROVED`, `OWNERSHIP_DENIED` or `FEATURE_NOT_CONFIGURED` when appropriate. UI disables or explains unsupported actions; it does not silently choose an insecure local implementation.

---

## 23. Data model / migration additions (append to §10)

The following are illustrative tables (use migration versioning; refine constraints and IDs in code). All user-owned resource keys must be checked in every SQL read/write and in all stream/download reattachments.

```sql
CREATE TABLE user_preferences (
  user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  push_mode TEXT NOT NULL DEFAULT 'off',
  voice_mode TEXT NOT NULL DEFAULT 'off',
  theme TEXT,
  updated_at TEXT NOT NULL
);

CREATE TABLE push_subscriptions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_id TEXT NOT NULL,
  endpoint_hash TEXT NOT NULL UNIQUE,
  encrypted_endpoint TEXT NOT NULL,
  encrypted_keys_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  revoked_at TEXT
);

CREATE TABLE push_delivery_events (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  subscription_id TEXT NOT NULL REFERENCES push_subscriptions(id) ON DELETE CASCADE,
  run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  event_kind TEXT NOT NULL,
  delivered_at TEXT,
  UNIQUE(subscription_id, run_id, event_kind)
);

CREATE TABLE chat_drafts (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  chat_id TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
  body TEXT NOT NULL,
  revision INTEGER NOT NULL DEFAULT 1,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (user_id, chat_id)
);

CREATE TABLE new_chat_drafts (
  user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  body TEXT NOT NULL,
  revision INTEGER NOT NULL DEFAULT 1,
  updated_at TEXT NOT NULL
);

CREATE TABLE prompt_templates (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  body TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(user_id, name)
);

CREATE TABLE user_budgets (
  user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  daily_soft_usd REAL,
  monthly_soft_usd REAL,
  daily_hard_limit_usd REAL,
  updated_at TEXT NOT NULL
);

CREATE TABLE message_search_index_source (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  chat_id TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
  turn_key TEXT NOT NULL,
  role TEXT NOT NULL,
  search_text TEXT NOT NULL,
  timestamp TEXT,
  PRIMARY KEY (user_id, chat_id, turn_key, role)
);
-- Implement FTS5 external-content or contentless index with stable rowid,
-- ownership-filtered joins and INSERT/UPDATE/DELETE triggers. Avoid a naïve
-- cross-user MATCH query that filters owner only after revealing snippets.
```

**Security qualifiers:** `encrypted_endpoint` and `encrypted_keys_json` must refer to real authenticated encryption with key material outside SQLite, **not** base64 or reversible encoding called "encryption". If an encrypted secret store is not implemented yet, put credentials in a suitable protected secret store and change the schema accordingly before shipping push. The `user_budgets.daily_hard_limit_usd` field must be nullable and UI-disabled unless server can enforce it accurately enough to honor the documented semantics. Search index text is duplicated *derived* sensitive data; apply the documented exception in §21.3 with retention, backup and delete semantics. `ON DELETE CASCADE` does not independently delete files in Hermes, indexes without correct triggers, Web Push subscriptions held by providers, or backups — coordinated cleanup is required.

---

## 24. Product flows, UI density, and operational edge cases

### 24.1 Approved-user journey

1. User visits `/`, signs in with Google, sees Pending / Rejected / Setup or active chat based on server policy. **No** Hermes calls from non-active states.
2. Active user lands on most recent conversation (or new-chat draft), with sidebar chat search/recent/pinned and clear New Chat; no cross-account snippets.
3. User chooses a supported model, types Markdown or starts voice, inserts slash template, attaches image/file and sends. Composer draft is cleared only after durable server admission.
4. Hermes emits streaming tokens, tool cards, human approvals, OpenUI and terminal status. If browser closes, run continues under server's ownership policy; generic push may notify completion if opt-in.
5. User reopens on another device, browses **turn-level** history (including compacted turns), searches own messages, clicks a result to scroll/highlight, resumes chat with correct Hermes session/profile, or exports selected turns.
6. Admin suspends user: close new API permissions and stream reconnects, cease push deliveries, block background tool actions/jobs per §§3/12; protect data while allowing later reactivation without cross-user leaks.

### 24.2 Mobile & desktop layout

- **Desktop:** left sidebar (new chat, recent/pinned/history/search), central chat, optional right drawer (model, context, tools, usage, settings); don't use a mandatory permanent third column on smaller screens.
- **Mobile/PWA:** full-width conversation, accessible drawer, sticky composer, safe-area insets, keyboard viewport adaptation, install flow, permission UI and swipe/scroll resilience. Do not block main thread with OpenUI charts during streaming.
- **States:** authenticating, pending, setting up, empty, loading history, indexing, streaming, blocked/approval required, error/timeout, offline, cancelled, unknown run, unsupported upstream, suspended. Never show a spinner forever without retry/exit.
- **Search & export:** content search result anchor must work even when its turn is not currently loaded. Export long chats without fetching everything into the browser first (incremental backend pages + streaming writer where practical).

### 24.3 Concurrent tabs and data correctness

- Two tabs share one account but do not produce duplicate Hermes runs from one retry. Draft revisions use last-writer policy with explicit conflict or compare-and-swap; never overwrite newly edited text silently.
- One chat with an active run may have multiple viewers; allow exactly one run admission where Hermes requires serialization. Reconnect resumes same app run ID. Tool approval must bind exact pending control and reject stale/time-expired decisions.
- Closing an app tab is not a signal to cancel agent. **Never send notification from a partial assistant delta**; only terminal state drives push. Run state `unknown` is not success.
- Crash/restart Chat UI must reconstruct run state via Hermes and reconcile push, search index, drafts, cost records and ownership using bounded jobs; no cross-account broadcast or session reassignment.

---

## 25. Feature-parity acceptance tests and release gating

All tests below require two approved isolated Google identities (`A` and `B`) and at least one pending/rejected account (`P`). Test `A/B` negative cases for every owner-scoped new endpoint and service-worker action.

| Test ID | Test | Expected pass condition |
|---|---|---|
| `UXP-ACC-001` | Install PWA, start offline, log out | App shell opens; no cached prior user's messages/media and no offline auth bypass |
| `UXP-ACC-002` | A opts in to push; run finishes while tab closed | A device receives generic notification once; B gets none; click reauths and deep-links into A's chat |
| `UXP-ACC-003` | Suspend A while run finishing | A receives no new sensitive push; A can't reattach events or fetch private content until active again |
| `UXP-ACC-004` | Search phrase inside 1,000-turn A chat including compacted fixture | Owned hits, stable pagination/anchor/highlight; B and P see zero A hits; partial index clearly labeled |
| `UXP-ACC-005` | B guesses A search/chat/draft/template IDs | Server returns denial, without result count, snippet or object metadata leakage |
| `UXP-ACC-006` | Record voice, cancel then transcribe supported sample | Cancel deletes staging asset; successful transcription appears **editable**, not auto-sent; unsupported Hermes speech is explicitly disabled |
| `UXP-ACC-007` | Editor with mixed Markdown, inline/fenced code, paste image, mobile IME | Text/formatting preserved, expected send/newline behavior, keyboard accessible, no payload execution |
| `UXP-ACC-008` | Draft across two devices and reload | Same user's latest revision restored; no bleed to another conversation/user; conflicts handled deterministically |
| `UXP-ACC-009` | `/template` create/use/update/delete | Owner only, autocomplete and insertion correct, text never executes a tool on selection |
| `UXP-ACC-010` | Chat has known/unknown usage and pricing | Known values accurate; unknown shown as unknown; estimated cost labeled estimate; no fabricated balance |
| `UXP-ACC-011` | Configured soft budget reached | New run gets warning; hard ceiling enforced only where actually supported; bypass by REST disallowed |
| `UXP-ACC-012` | Regenerate, edit and rewind with upstream support/no support | Correct fork/retry history and no corruption when supported; otherwise explicit unsupported state; no direct Hermes DB write |
| `UXP-ACC-013` | Steer active A run, then attempt steering B/finished run | A succeeds if upstream supports; B/finished rejected; no ghost assistant turn |
| `UXP-ACC-014` | Disconnect/reconnect 10 cycles during tools and OpenUI | One logical run/turn, correct terminal state, bounded SSE buffers, no data crossing users |
| `UXP-ACC-015` | Preview HTML with script/iframe/network attempts; SVG with event attributes | No script execution in Chat UI origin, credential exfiltration, direct Hermes/network access; safe download fallback |
| `UXP-ACC-016` | Export 1,000-turn own chat to MD and browser-PDF | Chronological correct text incl compacted turns, stable memory, usable print styling, private media handled by placeholder/owned assets |
| `UXP-ACC-017` | Mixed OpenUI/charts/media chat while searching and exporting | Lazy modules/thumbnails, no full-media prefetch, no cross-origin OpenUI actions; backend remains within §14 target or documented exception |
| `UXP-ACC-018` | Delete chat and re-login | Canonical Hermes session removal reconciles; app search/draft/assets/export references purged; stale signed URLs denied |
| `UXP-ACC-019` | Power-cycle/redeploy only Chat UI container | Hermes remains intact; chat/draft/search indexes/push preferences restore or safe-rebuild; no duplicated terminal notifications |
| `UXP-ACC-020` | Inspect generated production image and network | Single Go server + built React, existing Hermes in different container, no bundled runtime Python/Node process, no local Hermes mounts/DB writes or direct provider calls |

**Mandatory release evidence:** a versioned `docs/FEATURE_PARITY_AIXELL.md` table tracking each `UXP-*`, `LLM-*`, `CTX-*`, `MEDIA-EXT-*`, `QUEUE-*` and `KNOW-*` requirement as `IMPLEMENTED / TESTED / UPSTREAM_BLOCKED / OWNER_ACCEPTED_EXCEPTION`, file paths, API fixtures, test IDs and measured performance. All `P0` security controls must pass regardless of owner exceptions. No claim of completed parity if rows are only mock UI or unverified Hermes integrations.

**Suggested execution order:** Security foundation (§§3–5) → owned chat/turn streaming/history (§§6–10) → rich composer/templates/drafts/search → usage/cost → PWA/push → safe previews/export → verified rewind/steer/voice integration → performance hardening (§14), with parallel UI tests for every feature. The **deployed Hermes container must not be replaced/reconfigured by Chat UI code** unless owner separately approves a documented operator task.


---

## 26. Exhaustive Aixell product capability coverage — v2.4 addendum (normative)

**Purpose:** §21 captured the initial mature chat UX features but is **not** a complete inventory of the user-facing capabilities observed in the supplied `aixell-main.zip`. This section closes that gap. **All user-facing functional behavior listed in §§21 and 26–29 is in product scope.** The security, single-assistant, existing-Hermes-container, Go/React, Google OIDC/admin approval, and per-user isolation decisions in §§1–20 **override** implementation techniques used by Aixell.

**Important wording:** "In scope" does not mean an endpoint is available or a feature is already implemented. For Hermes-dependent features, the UI MUST have a deployed-version API probe, capability flag and safe disabled/blocked state. A *fully complete Aixell parity release* requires `TESTED`, or a documented explicit owner-approved exception for a specific feature. Never silently substitute fabricated data, shared profiles, direct Hermes file/database manipulation, or direct provider requests from Chat UI.

### 26.1 Feature-to-spec coverage and release gate

| Domain | Aixell observable user-facing behavior | Requirement in our Chat UI | Priority / integration gate |
|---|---|---|---|
| Authentication | Login/logout, logout all devices, account safety | Google OIDC, admin approval, sign out all sessions, session/device list where possible | P0; our stronger RBAC prevails |
| Agent selection | Pick among configured Hermes profiles | **One logical assistant**; user never chooses another user's Hermes profile | P0; deliberate divergence, not missing feature |
| Session/history | List, title, archive/pin, reopen, multi-source views | All own conversations, pin/archive, rename/delete, turn-level history with compaction, reliable reload/pagination | P0/P1; Hermes transcript truth |
| Streaming | Text/Markdown, tool progress, reasoning/status, cancel, approval, reconnect | SSE with message/tool boundaries, trusted commentary-only display, run identity, approval, resume/reconcile | P0 |
| Model selection | Model, provider, context window, capability/pricing metadata | Discover deployed Hermes model options; model/provider picker; per-chat persisted selection and reset | P1; allowlisted, user-scoped |
| Advanced model settings | Reasoning effort, service tier, hide models | Capability-filtered reasoning levels, service tier, hide/show locally in own picker | P1; never assume provider accepts all values |
| Rich model metadata | Input/output modalities, reasoning support, price, quotas | Visual badges for vision/audio/video/text, context window, supported options, provenance of prices/quotas | P1; unknown shown as unknown |
| Context/usage | Context bar, cache/reasoning/input/output tokens, call totals | Per-turn/per-session/per-user usage, last-call context occupancy, warnings and provenance | P1; no invented metrics |
| Provider account usage | Provider windows, balance, subscription quota; Kenari example | Show account quota/balance **only if Hermes/admin-approved read-only integration provides it** | P1 capability-gated; never read provider secret from UI |
| Budgets | Estimated cost, thresholds and chart | Soft user budget, enforceable configured hard-admission cap, daily/period graphs, model breakdown | P1 |
| Multimodal composer | Image/file picker, camera, paste, drag-drop, attach chips | Image/vision/file input with secure upload, thumbnails, retry, cancellation, limits and mime validation | P1; routed via Hermes |
| Generative media | Inline images, video previews, audio/TTS, asset lightbox | Owned gallery for Hermes tool outputs, job/progress, playback, download, range requests, expiry | P1; no imaginary media REST API |
| Voice | Recording/dictation, STT language, voice-mode choice | Mic with record/dictate modes, `id`/`en`/`auto` language preference, edit transcript before sending | P1; Hermes-side STT or verified alternative only |
| Interactive responses | OpenUI/visualize, charts/tables/cards | Safe OpenUI components and **per-chat Visualize toggle** with persisted state and safe Markdown fallback | P1; actual Hermes instruction compatibility |
| Composer | Rich Markdown editor, templates, drafts, clipboard/code | Syntax-preserving Markdown composer, slash templates, own drafts, copy/cite/code controls | P1 |
| Message queue | Queue, remove/reorder/send queued prompts, paused queue after failure/stop | Per-chat pending message queue with visible state, safe dispatch and durable deduplication | P1; §27.4 |
| Turn controls | Regenerate/retry, edit/rewind, active steer | Correct supported turn semantics, non-destructive branches where native rewind not available | P1 upstream gate |
| Memory/knowledge | Facts, MEMORY.md, USER.md, skills browsing/deletion | Private long-term memory/knowledge center, safe inspect and supported changes | P1; §28, many writes upstream-blocked |
| Persona | Editable web-agent personality, SOUL read-only | Per-user additional instructions (validated supported Hermes mechanism), base agent identity read-only | P1 capability gate |
| External chat | Telegram read-only browsing and copying/importing | User-authorized optional **read-only** external chat import; never connect an arbitrary shared Telegram profile | P2 or separately approved P1 |
| PWA/mobile | Install, push, share target, pull-to-refresh, viewport/sidebar | Installable responsive PWA, notification settings/tests, OS share-to-draft, mobile pull-to-refresh | P1/P2, browser capability gate |
| Appearance | Theme, font size, focus, side pane resizing | Per-device theme/font/focus, accessible resize, responsive drawers, reduced motion | P1 |
| Artifacts/export | HTML/SVG sandbox, Markdown/PDF, viewer/lightbox | Isolated preview and source/owned download, MD export, print CSS/PDF; no script execution | P1 security-gated |
| Diagnostics | App version/update controls and browser diagnostics | Release/build indicator, safe reload/update prompt, user-visible connection troubleshooting; diagnostics redacted | P1 |
| Scheduled/agent operations | Hermes jobs, skills, tools and approval event UX | Capability-driven user-scoped jobs/tools panel; P0 authorization policies still apply | P1 upstream gate |

**Excluded from reproduction by design:** Aixell-specific systemd/Cloudflare services, Hermes profile restore/sync scripts, hard-coded provider adapters or credentials, unrestricted filesystem knowledge editing, cross-profile linked memory, local Python for rewind/STT, any multi-agent selector allowing cross-tenant profile access. We preserve user-visible outcomes where authorized and technically supported, not those unsafe implementations. User-facing excluded capabilities require `OWNER_ACCEPTED_EXCEPTION` if strict parity is requested.

## 27. Complete LLM and multimodal control contract

### 27.1 Advanced model picker (`LLM-*`)

- `LLM-001` (P1): Fetch the **Hermes deployed version's** documented `GET /api/model/options` and `GET /v1/capabilities` (and other verified sources listed in §9A). Present providers, model identifiers, current default, availability/configuration status, supported input/output modalities, context window, reasoning support, source of metadata, and per-model allowed choices. Do not substitute bare `/v1/models` as a full model catalog.
- `LLM-002` (P1): A user's selection is saved **per owned chat**, with optional user default. A newly created chat starts from their default/current permitted profile model, **not** a different chat's persisted model. Reset-to-default works. Changing model must not rewrite earlier turns. Backend validates model/provider against an explicit per-user allowlist, provider credentials remain in Hermes.
- `LLM-003` (P1): Show reasoning-effort chooser for models/providers which support it; values are obtained or validated from Hermes capability, not guessed. The Aixell reference offers choices ranging from `none` to `ultra`, but the UI MUST only send *actually supported* enum values for the deployed Hermes + provider + model combination. Unsupported selection is disabled with explanation. Do not present hidden/private reasoning tokens or private chain-of-thought as user-visible transcript; only Hermes-authorized summaries/tool commentary.
- `LLM-004` (P1): Service-tier chooser (e.g. default/priority **if** supported). Preserve per-chat preference and show whether a provider/model supports priority. Never promise speed improvements or cost if no verified evidence. If API lacks per-run tier controls, mark unsupported instead of putting unused option in request payload.
- `LLM-005` (P1): User can hide/show models **only in their own picker**; admin may apply global policy allow/deny. Hiding a model never mutates Hermes' global configuration or a different user's list. An actively selected model hidden from picker remains shown with a recover/reset flow, unless administrator has revoked it.
- `LLM-006` (P1): Model badges/picker detail show known `text/image/audio/video` input-output modalities, reasoning support, context window, cache pricing, model notes and last refresh source/time. Unsupported/unknown metadata must display `Unavailable`, not an inferred capability. Hermes tool-driven image/video/TTS generation is **separate** from model-native multimodal capability.
- `LLM-007` (P1): Searchable model picker, refresh-on-open, clear unavailable/deconfigured model behavior, and accessible keyboard navigation. Keep normal chat functional when the metadata catalog is unavailable but a verified default still works; do not route to arbitrary user-provided model URLs.
- `LLM-008` (P1): Optional per-chat **Visualize/OpenUI** toggle persists independently; when off, standard Markdown remains usable. When on, the backend adds a versioned OpenUI instruction through a **verified Hermes per-request interface**. The UI renderer alone does not grant OpenUI generation ability. Unknown/partial OpenUI produces safe fallback.

### 27.2 Context, compaction, costs and provider quotas (`CTX-*`)

- `CTX-001`: Context bar uses the **last comparable model prompt** token count divided by the **effective model's verified context length**. Cumulative run tokens are not automatically context occupancy. If only run-level aggregate is returned, label it `Estimate` or omit the percentage. Visual warning tiers can be set at 70% and 90% when the ratio is valid; never imply guaranteed remaining capacity.
- `CTX-002`: Display context compaction indicator and retain *all* user-visible historical turns, including compacted/archived turns when Hermes exposes them. Never equate `active context` with total historical chat. If a subset cannot be recovered, show `History partially unavailable` and do not fabricate missing messages.
- `CTX-003`: Track input, output, cache-read, cache-write, reasoning, API-call counts, time window, approximate/actual costs, and by-model/provider breakdown **when returned by Hermes**. Show data provenance, timestamp, currency and whether estimated. Historical usage for own chats only; generic admins get aggregate safe metadata, no raw personal transcripts.
- `CTX-004`: Account/provider spend/quota windows (e.g. rolling 5h/weekly/monthly or balance) are optional **read-only** views. Hermes `GET /api/model/options` / provider usage metadata may supply some fields, but no arbitrary direct provider key loading or network query from Chat UI is authorized. Mark `PROVIDER_USAGE_UNAVAILABLE` when absent.
- `CTX-005`: Budget warnings versus hard enforcement must be visibly distinct. A hard limit is a *run admission gate* for measured/controlled usage; do not promise an exact provider billing cap for in-flight tool calls. Admin caps override user preference. Context meter and billing use independent measurement fields.

### 27.3 Complete multimedia input/output (`MEDIA-EXT-*`)

- `MEDIA-EXT-001`: Composer supports photos, image clipboard/paste, drag/drop, file selector, and camera capture where browser supports it. Each upload has file name, type, size, thumbnail/preview, progress, retry/delete, and ownership-bound asset ID. Validate file magic bytes, reject spoofed MIME, limit pixels and byte size to configured per-user budgets. Do not make uploads publicly cacheable.
- `MEDIA-EXT-002`: **Vision** is exposed only for chat models that can actually receive images via verified Hermes input protocol. A `image_generate` agent tool is not proof of model vision support. If model is text-only, offer an explicit model-switch suggestion or disable image-send; don't silently discard images.
- `MEDIA-EXT-003`: Generated images have thumbnails, zoom/lightbox, captions/provenance if provided, ownership-authorized download and stable historical replay where the asset still exists. Asset URLs must be owned/proxied, scoped and checked; no direct blob listing or path traversal.
- `MEDIA-EXT-004`: Generated video is an asynchronous job/long-running tool when required by provider. Show pending/progress/completed/failed state without inventing percent/time, preview/poster when available, playback, download, retry as a new deliberate action; do not declare success on `tool.started`.
- `MEDIA-EXT-005`: Audio/TTS output player supports play/pause, duration if known, download of owned media and reload. Voice input has editable dictation transcript, recording preview, language choice `id/en/auto` when supported, mic permission errors and no auto-send. Tool TTS and a native `/v1/audio/speech` endpoint must not be conflated.
- `MEDIA-EXT-006`: Expose tool activity for web search, citations, attachments, image/video/audio outputs, elapsed time if measured, intermediate statuses and completion/error. Never show provider credential, unsafe internal URL or private raw tool arguments. Media regeneration is a **new run** with idempotency and clear charges.
- `MEDIA-EXT-007`: Rich OpenUI, charts, tables, audio/video, and sandbox previews use browser-side on-demand components and media lazy loading; server does bounded byte streaming with range requests where needed. Multimedia size and concurrency must be part of §14 benchmarks.

### 27.4 Durable per-chat queued messages (`QUEUE-*`)

- `QUEUE-001` (P1): If an owned Hermes chat already has an active run, user may queue the next message(s), see ordinal position, edit/remove pending items, pause/unpause automatic sending, and manually `Send next` only after previous run has ended. Never call Hermes twice concurrently against a serialized session.
- `QUEUE-002`: Queue is **scoped by authenticated user + chat**, not browser-global; persist metadata and attachment references in Chat UI SQLite so refresh and device switch are safe. Limits per user/chat prevent unlimited storage; retained queue content follows user data retention and logout policies. The UI may keep optimistic local drafts but server is authoritative.
- `QUEUE-003`: A queued item moves to `SUBMITTING` exactly once with transaction-backed compare-and-swap + stable idempotency key, after rechecking approval, profile binding, chat ownership, attachment validity, rate/budget limits and run state. Automatic dispatch pauses on run failure, cancellation, `unknown` state, suspension or Hermes unavailability; requires explicit user recovery, not blind retries of side-effecting turns.
- `QUEUE-004`: Do not confuse `steer run` (native in-flight guidance) with `queue next message` (normal new turn after terminal). If a steer arrived too late, only enqueue its contents after showing the user a clear status, without a duplicate effect.

## 28. Per-user memory, knowledge, skills, personality and optional external history

### 28.1 Private knowledge center (`KNOW-*`)

- `KNOW-001` (P1): Provide one user-visible **Memory & Skills** panel with long-term facts, safe stored notes/profile preferences, own enabled/learned skills and memory status. Data is derived from **that user's mapped Hermes profile** and must never use Aixell's shared `default` memory DB or unrestricted direct filesystem access.
- `KNOW-002` (P1): Read-only **memory facts** list, search, source/type when upstream provides it, freshness and paging. Edit/forget/delete fact is offered **only when a documented and tested per-profile Hermes API exists** and authorization provides isolation all the way to tools/storage; otherwise display `Upstream memory edit unavailable` and link to an operator-controlled workflow. No direct read or write of `memory_store.db` or custom Python scripts in Chat UI.
- `KNOW-003` (P1): Stored note sections analogous to `MEMORY.md` and `USER.md` may be displayed/edited only through explicit documented Hermes authenticated API that is scoped to the user's state. `SOUL.md` or base agent policy remains read-only where such inspection is explicitly supported; user text cannot override policy/admin guardrails. Do not mount profiles into Chat UI to implement notes.
- `KNOW-004` (P1): List/search skills with name/category/description, show their content in safe text/Markdown where available. Enable/disable or delete learned custom skill only through verified authorized Hermes skill-management API with owner/per-profile binding. Built-in/admin skills cannot be deleted by normal users. The existence of `GET /v1/skills` does **not** imply write/delete endpoints.
- `KNOW-005` (P1): Per-user **additional personality/instructions**, if Hermes has a supported scoped per-request or per-profile override, stored as user-owned preference and applied on subsequent chat turns only. Display base assistant policy separately and clearly mark additional instructions as lower-priority. No direct update to a shared `SOUL.md`; no cross-account inference of hidden agent settings.
- `KNOW-006` (P1): Memory edits must not be treated as complete just because the UI updated optimistically; confirm via Hermes read-back or terminal outcome. Wipe/delete account workflows coordinate Hermes upstream erasure; a UI deletion and account suspension alone do not prove memory erased.
- `KNOW-007` (P1): If Hermes exposes only coarse session/profile APIs, mark viewer/manager gaps `UPSTREAM_BLOCKED` with endpoint/version evidence; do not invent `/api/knowledge/facts` or `/v1/memory` as native Hermes endpoints. Support user-requested memory actions *through the agent chat itself* where the configured Hermes tool reliably enforces policy, but do not claim that is structured direct memory editing.

### 28.2 External history (`IMPORT-*`)

- `IMPORT-001` (P2, or P1 with separate owner sign-off): Offer optional **read-only Telegram/external-chat import** like Aixell, but only for a chat history the **current user has independently authorized**. Do not expose an existing Hermes operator's global/default Telegram sessions to all Google users. Account linking and consent/revocation are required before viewing any external message. The feature stays disabled when the existing Hermes deployment offers no safe per-user channel API.
- `IMPORT-002`: Import is non-destructive: show source label and preview, select explicit messages, create *a new user-owned Hermes chat* with clear source annotation only when supported, never silently graft private Telegram history into another user's session. Imported text never creates the right to use someone else's memory/profile.

## 29. Additional UI/API routing and parity acceptance tests

### 29.1 Chat UI Go endpoints — **application API, not claimed Hermes APIs**

| UI capability | Chat UI endpoint contract | Hermes upstream / source | Safe behavior when unavailable |
|---|---|---|---|
| Model catalog & capabilities | `GET /api/models`, `GET /api/models/{id}/capabilities` | `GET /api/model/options`, `GET /v1/capabilities` plus verified metadata | Partial catalog; unknown metadata marked |
| Per-chat model options | `GET/PUT /api/chats/{id}/settings` | Verified per-run/session request options and app persisted prefs | Explicit unsupported field, no silent no-op |
| Own model hiding | `GET/PUT /api/me/model-visibility` | Chat UI SQLite per-user preference | Never changes Hermes provider config |
| Context and model usage | `GET /api/chats/{id}/context`, existing usage endpoints (§22) | Hermes run/session usage, verified model context sizes | Unknown; no synthesized quota |
| Provider quota read-only | `GET /api/me/provider-usage` | Only documented Hermes/approved read-only provider info | `PROVIDER_USAGE_UNAVAILABLE` |
| Queue | `GET/POST /api/chats/{id}/queue`, `PATCH/DELETE /api/chats/{id}/queue/{itemId}` | Chat UI SQLite + Hermes run terminal state | Paused while unsafe/disconnected |
| User persona | `GET/PUT /api/me/personality` | App-owned preference + verified Hermes instruction mechanism | Saved as inactive until Hermes supports |
| Memory facts | `GET /api/me/memory/facts`, `PATCH/DELETE /api/me/memory/facts/{factId}` | **Only verified upstream per-user memory APIs** | Read-only/disabled with upstream code |
| Notes | `GET/PUT /api/me/memory/notes/{kind}` | **Only verified upstream per-user notes APIs** | Display status only if unavailable |
| Skills | `GET /api/me/skills`, `GET /api/me/skills/{id}`, `DELETE /api/me/skills/{id}` | Hermes `GET /v1/skills` if verified; safe user write only if exposed | Read-only; delete unsupported |
| External history | `GET /api/me/import/sources`, `GET /api/me/import/{source}/chats` | User-authorized upstream integration | Disabled until independent consent verified |

**Request-scoped security contract:** Every endpoint above enforces `can_chat` (or the narrower permitted account-status equivalent), Google session, role, ownership and per-user Hermes profile binding. Model `provider`, `profile`, `endpoint`, `apiKey`, asset path, memory storage path, session ID and source-import identity supplied by the browser are **not** authority. Add new storage tables (`chat_settings`, `user_model_visibility`, `message_queue`, `user_personality`, `external_source_grants`, scoped memory audit) to §23 migrations, with all owner IDs, uniqueness, CAS revision, deletion and indexes. Never persist Hermes secrets, user provider tokens or full prompts in operational logs.

### 29.2 Acceptance suite (`LLM-ACC-*`) — required for full UX parity

| ID | Expected end-to-end behavior |
|---|---|
| `LLM-ACC-001` | A and B see only provider/models permitted for their independently mapped profiles; B cannot request A's model, profile or session by modifying JSON, headers or paths |
| `LLM-ACC-002` | A selects model + supported reasoning effort + service tier; on reload settings persist only for A/chat; run uses verified submitted settings; rejected unsupported enum never silently proceeds |
| `LLM-ACC-003` | A hides a model; B still sees it unless global admin policy independently blocks it; A can reveal/reset, and global policy always wins |
| `LLM-ACC-004` | Model with known context & prompt usage shows mathematically sound context percentage; unknown or aggregate-only tokens show Unknown/Estimate; warning tiers not mistaken for provider truth |
| `LLM-ACC-005` | A reopens compacted chat; full recoverable turns and media remain in sequence; context occupancy reflects live prompt, not whole transcript |
| `LLM-ACC-006` | A opens picker for text-only, vision and media-capable models: badges accurate; image input disabled when unsupported; image generation tool alone does not claim vision support |
| `LLM-ACC-007` | Image/video/TTS from Hermes show correct owned artifact/progress and reload; B cannot access URL by guessing IDs or Range headers; missing media renders placeholder |
| `LLM-ACC-008` | A records voice, chooses STT language, gets editable transcript, and deliberately sends; cancel/deletion cleans staged assets; no hidden direct provider request |
| `LLM-ACC-009` | A sends OpenUI-enabled turn with safe renderer; toggle off uses Markdown; malformed rendering does not corrupt conversation or execute model-supplied actions |
| `LLM-ACC-010` | A queues messages during an active run; messages have stable order, one dispatch per item, pause on stop/failure/unknown, no duplicate on refresh or concurrent tabs; B cannot see A's queue |
| `LLM-ACC-011` | A opens memory facts/notes/skills; reads only A data; unauthorized write receives upstream-blocked error rather than touching filesystem; admin cannot read content by default |
| `LLM-ACC-012` | A sets personality; applied only through verified lower-priority Hermes instruction for A's following turn; B sees none; no global SOUL or cross-profile mutation |
| `LLM-ACC-013` | Provider balance unavailable in Hermes results in clear Unavailable; dashboard never fabricates actual balance, billed amount or model price |
| `LLM-ACC-014` | External Telegram history unavailable without independent per-user consent; no other Hermes operator's chats show up in list/search/import |
| `LLM-ACC-015` | Source tree/production image contains no direct profile mounts, local Hermes SQLite reads, local `ffmpeg`/Python media pipeline, remote LLM provider requests from Chat UI or arbitrary Hermes proxy route |
| `LLM-ACC-016` | Verify PWA share-to-draft, push threshold/device settings, session sign-out-all, theme/text-size/focus/sidebar preferences, app updates, search and safe export with A/B privacy tests |

### 29.3 Full parity tracking and sign-off

Implementation MUST maintain `docs/FEATURE_PARITY_AIXELL.md` listing **every row in §§21, 26, 27, 28 and 29** with `source_module`, `UX/API contract`, `priority`, `owner`, `Hermes version/capability observation`, `IMPLEMENTED/TESTED/UPSTREAM_BLOCKED/OWNER_ACCEPTED_EXCEPTION` status, and code/test evidence. **Unsupported upstream does not count as delivered feature.** A secure beta can ship before P1 parity, but full-parity release requires all required features tested or consciously waived by owner. P0 auth, approval, isolation, least privilege and tool sandbox gates cannot be waived by default. Record explicit **deliberate divergences** (single logical assistant, no arbitrary profiles, no shared memory, no direct Hermes DB scripts) rather than mislabel them forgotten functionality.

Minimum Hermes probe before feature planning: check deployed `GET /v1/capabilities`, `GET /api/model/options`, session/message APIs incl. compacted history, model override/effort/tier fields on real runs, run steering/approvals, multimodal upload input format, returned artifact layout, memory/notes/skills read/write exposure, provider quota reporting and voice STT. Store harmless fixture shapes (redacting secrets) and update the table rather than relying on README or another Hermes deployment.

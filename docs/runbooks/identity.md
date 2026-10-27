# Runbook — identity, sessions and screens (slice 2)

Code: `internal/auth`, `internal/store/accounts.go`. Governs PRD §3, §4, AUTH-001…004, 007.

## Login

`GET /api/auth/google/start` → Google → `GET /api/auth/google/callback`. Authorization Code with PKCE
(S256), scopes `openid email profile` only. `state`, `nonce` and the PKCE verifier live in memory,
single-use, 10 minutes, capped at 4096 in flight; a restart drops in-flight logins (the user signs in
again). A `__Host-antares_oidc` cookie binds the flow to the browser that started it (login-CSRF).

The ID token is verified locally: RS256 only (`none`/HS* refused), key from Google's JWKS by `kid`
(cached 1h, refetch on unknown `kid` at most once a minute), `iss` ∈ Google's two issuers, `aud` =
our client id (`azp` required when there are several), `exp`, `iat`, `nonce`, `sub`, `email`,
`email_verified`. Any failure is one redirect to `/error?code=login_failed`; the reason goes only to
`audit_log` (`auth.login`, outcome `rejected:<reason>`, no actor). The Google access token is
discarded unread. Endpoints are fixed constants, not environment-configurable.

Identity is `(google_issuer, google_sub)`; email is refreshed on each login and never used to find or
authorize an account. A new identity becomes `USER / PENDING / UNPROVISIONED` and nothing else: no
tenant binding, no key, no Hermes call. A login never changes role or status of an existing user.

## Sessions

Opaque 32-byte token in `__Host-antares_session` (`HttpOnly; Secure; SameSite=Lax; Path=/`). The
database holds only its SHA-256. A new token is issued on every login and the browser's previous
session is revoked. Absolute lifetime 7 days (no idle extension yet). Logout is `POST
/api/auth/logout`, same-origin only (`Origin` must equal `ANTARES_PUBLIC_ORIGIN`), sets `revoked_at`,
clears the cookie. Every request re-reads the user row: a status change applies on the next request.

**Sign out everywhere** (#150) is `POST /api/auth/logout-all`, same origin rules: it revokes every live session of
the caller's own account (`store.RevokeUserSessions`; the account comes from the session, never the request), audits
`auth.logout_all`, and clears the cookie. Every other device's next request is a 401 and has to sign in again, and
the account's push subscriptions are removed in the same transaction, so a signed-out device stops getting alerts. An open
run event stream ends with `run.disconnected` without sending another event. Each stream registers itself with its
user and session and then re-checks the session once in the database, so a sign-out that landed between the guard and
the stream's start is caught. `auth` then tells the chat service of every sign-out (`SessionsRevoked`), which marks each
matching open stream under the lock that stream writes its batches under: once the sign-out answers, no stream starts
another batch for a signed-out session. A batch already being written finishes first (the sign-out waits for it, at most
2 s per stream, so a client that stopped reading cannot hold it open). The heartbeat still reads the database
(`auth.SessionLive`), which covers revocations made elsewhere; only a session the database says is gone counts, and the
read is not cancelled with the request (`TestSignOutBetweenGuardAndStreamStartSendsNothing`,
`TestSignOutWaitsForTheBatchInFlight`, `TestRevokedSessionStopsAContinuousStream`). The web app offers it in the account menu.

**An admin can do the same for a user** (#113): `POST /api/admin/users/{userId}/revoke-sessions` (admin guard, same
origin, no body) revokes every live session of that account and its push subscriptions, stops its open streams the
same way, and audits `admin.revoke_sessions` with actor, target and outcome only. The account, its chats and its
workspace are untouched: the person signs in again (`TestAdminRevokeSessionsSignsTheUserOutEverywhere`). The user
screen offers it under **Sessions**. The audit screen's **Export audit log (JSON)** walks every page of
`GET /api/admin/audit` and saves the metadata it returns (no content); a log longer than 400 pages is refused, never
exported in part.

## Appearance and diagnostics (#113)

The account menu has two per-device panels. **Appearance** (theme, text size, conversation-list width, focus mode) is
kept in this browser's local storage (`antares:appearance:v1`) and applied as `data-*` attributes on `<html>` before the
first paint; "Follow the system" is the default, and an OS reduced-motion setting is always honoured. **Diagnostics**
shows the server build (`GET /api/version`, public: `{"revision": "<commit>"}` stamped at image build from
`REVISION`, `unknown` for a local build), the bundle (`/source-hash.txt`), network and server readiness (`/readyz`)
with its latency, live-reply support and the app-update state. It reads nothing else: no cookie, token, account
field or upstream key (`web/src/Settings.test.tsx`).

## Guards

`Active` (every chat-touching route) = valid session ∧ `APPROVED` ∧ `tenant_status = READY` ∧ a
`READY` tenant binding row exists. It also rejects caller-supplied profile / route prefix / key /
session-id query parameters and headers. `Admin` = valid session ∧ `ADMIN` ∧ the same eligibility as `Active` (`APPROVED`, tenant `READY`, ready binding; PRD §3.1 "Admin (active and mapped)"). An approved admin with an unprovisioned or failed tenant gets 409 `TENANT_NOT_READY`; the admin's own workspace is provisioned out of band first. Routes are
listed in `auth.ChatRoutes`; a test parses the PRD §9 and §9A.7 tables and fails if a listed route
is not guarded. Until the slice that implements a route lands, an eligible user gets 501
`CAPABILITY_UNAVAILABLE` from it. The live Hermes health check joins the gate in the tenant slice.

## Screens

The status screens (login, pending, rejected, suspended, setting-up, error, and a placeholder for an
active account) are React (`web/src`, `Screen.tsx`). The app calls `GET /api/me`: 401 means signed out,
otherwise `state` picks the screen. An unexpected response is the "could not reach Antares" screen, never
a guessed state. The server serves the shell for every non-`/api` path; the guards live only on the API.

The bundle is built to `internal/webui/dist` and **committed**, then embedded with `embed.FS`, so
`go build` needs no Node and production runs none. After changing anything under `web/`:

    cd web && npm ci && npm run build   # also: npm run typecheck && npm test

and commit `internal/webui/dist`. `go test ./internal/webui` fails if the sources and the bundle disagree
(`dist/source-hash.txt`), and CI rebuilds and fails on any diff. `ANTARES_STATIC_DIR` serves a directory
instead of the embedded bundle (development only).

## Not here yet

Per-IP rate limiting of the callback, idle session expiry, reject/suspend reasons on screens (the
schema has no column for the user-facing reason), admin approval endpoints and UI.

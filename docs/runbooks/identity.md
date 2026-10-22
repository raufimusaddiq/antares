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

## Guards

`Active` (every chat-touching route) = valid session ∧ `APPROVED` ∧ `tenant_status = READY` ∧ a
`READY` tenant binding row exists. It also rejects caller-supplied profile / route prefix / key /
session-id query parameters and headers. `Admin` = valid session ∧ `APPROVED` ∧ `ADMIN`. Routes are
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

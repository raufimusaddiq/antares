# Quickstart: validating blog discovery

Prerequisites: toolchains as in `AGENTS.md`; browser-check harness (`scripts/browser-check.mjs`).

1. **Server**: `go test ./internal/blog ./internal/auth ./internal/server` — covers `limit`, `q` (including `%`, `_`, `\`, very long, empty), stable paging, approved-only rows, identical response for anonymous and signed-in callers, and the unchanged `PublicBlogRoutes`.
2. **Unit**: `cd web && npm test` — strip, entries on every screen, reader state, list search / Show more / retry / Yours.
3. **Browser**: `ONLY=blog-entry node scripts/browser-check.mjs` (sign-in with strip, pending screen, chat menu and rail) and `ONLY=blog-list` (search, paging, Yours) at 320/390/820/1280, light and dark; the layout width check must pass.
4. **Manual (production after deploy)**: signed out → sign-in → strip → a post → Sign in; pending account → Read the blog → Back to Antares lands on the waiting screen and chat stays closed; approved user → menu → Read the blog → find another user's post → Back to Antares lands in the chat. Time SC-001/SC-002.
5. **Security**: as a pending account, `GET /api/blog/posts` still answers 401/403; the public list is byte-identical with and without a session cookie; nothing in `/api/admin/*` lists readers.

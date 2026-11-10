# Quickstart: validating Admin Full Control

Prerequisites: toolchains as in AGENTS.md (Go, Node 22), a built web bundle.

1. **Tests**: `go test ./...` and `npm test` in `web/` — new tests named for FR-001…FR-023 must fail before and pass after.
2. **Takedown (US1)**: with the dev fixtures, publish a post as user A. As admin: list → open → take down with a reason. Check as a visitor (allow up to 60 s of cache, 5 min for images): `/b`, `/b?q=`, landing latest posts, `/b/{slug}` (404 identical to a missing slug), link-preview HTML. As A: posts page shows Taken down + reason, Publish disabled, banner once, not again after reload or on another device. Restore: all surfaces return, same address and date. Audit shows both actions.
3. **Blindness (SC-007)**: with a draft, an unpublished version and a post check present, every admin response is scanned for their text; none appears.
4. **Models (US2)**: switch a model off: gone from the picker; a chat on it answers `MODEL_UNAVAILABLE`; title, summary and post check do not use it. Try to switch off a default: refused naming purposes.
5. **Defaults (US3)**: set chat/summary/blog assistant defaults; new chat, existing chat with no saved model, a title, a shared-picture summary and a post check follow them; a chat with its own model keeps it. Make a default unusable (remove from fake catalogue): fallback + warning.
6. **Browser**: `ONLY=admin-posts`, `ONLY=admin-models`, `ONLY=blog-editor node scripts/browser-check.mjs` (Node 22 PATH), all green at 320/390/820/1280, light and dark.
7. **Production** (after merge): backup, deploy, smoke with a fixture author and post; clean up fixtures; never touch real accounts.

# Handover: blog feature

Split of work after the design was approved (PR #274). Design and pages are done in the figcraft session;
**backend and Hermes work goes to a separate session that has no FigCraft.** That session needs only this
folder, `docs/design/README.md` and the PRD. It never needs to open the `.figcraft` file.

## State

- Design approved by the owner (T011 ticked). The `.figcraft` file is the source of truth for look and flow;
  read `docs/design/README.md` and `contracts/design-pages.md` for the frame and flow list in text form.
- Setup branch `feat/blog-setup` (local worktree, not pushed): T012 empty packages, T013 TipTap 3.33.0 pinned
  (MIT; about +424 KB minified, +133 KB gzip over bare React as a standalone bundle; lazy-load on the editor
  route only). `internal/blog/doc.go` there is a placeholder; T016 replaces it.
- Not done: everything from Phase 3 on. No Go code exists yet.

## Backend and Hermes session (needs a Go toolchain, a Hermes, no FigCraft)

Tasks: T014 (fixtures, **blocker for US2/US3**), T015-T020, T021-T025, T027-T030, T039-T042, T044-T048,
T052-T053, T055, T057, T060 (server part), T063, T065, T068 (server part), T071, T072, T074, T075 (owner approval only).

Rules that apply:
- `AGENTS.md` and `docs/runbooks/security-invariants.md`: default deny, backend selects the profile,
  ownership on every query, admin blind. The public read surface is the one exception (Complexity Tracking in `plan.md`).
- T014 needs a live Hermes. Record real shapes with keys redacted; do not guess them. Update
  `contracts/check-envelope.md` to the recorded shape. Open risks R3 (run without a visible chat) and
  R4 (does Hermes expose the URLs web search returned) decide whether sourced data can be marked verified.
- Do not touch production without the owner's explicit approval.

## Page session: built, on branch `feat/blog-pages` (local, commits dae36775 and c48ca43c)

The pages exist as code in `web/src/blog/`, built from the approved frames against `contracts/http-api.md`. The
backend does not exist yet, so none of it has run against a real server; it ran only against a mocked API in the
browser (desktop dark, phone 375 px, no sideways scroll) and in vitest tests (see the PR for the count).

| File | What it is |
|------|------------|
| `types.ts`, `blogApi.ts` | API shapes, strict parsers (https-only sources, unknown state is not a state), one request helper; `409` carries the latest post |
| `autosave.ts` | compare-and-set autosave, local copy kept until the server confirms, conflict and offline states |
| `Editor.tsx`, `PostSettings.tsx` | TipTap editor (lazy-loaded), toolbar, image upload, settings sheet with the address rules |
| `PostList.tsx` | My posts: empty, loading, error, status chips, row actions, delete confirm |
| `PublishFlow.tsx`, `Sources.tsx` | choose, check (polls `GET .../check`), preview with accept/reject, unverified sources need an acknowledgement, every failure worded as itself |
| `DocView.tsx`, `ReaderBlock.tsx`, `Public.tsx` | document renderer (no HTML path, https/mailto links only), reader block (all actions ignored; plain-text fallback), public `/b` and `/b/<slug>` |
| `BlogApp.tsx` | author routes `/blog`, `/blog/<id>`, `/blog/<id>/publish`; wired in `App.tsx`, `main.tsx` (public routes) and the rail button in `Chat.tsx` |

Things the backend session must match (the page code assumes them; change the page if the real shape differs):
- `GET /api/blog/posts/{id}` returns `{id,title,summary,slug,coverUrl,status,latestVersion,publishedVersion,doc,check,updatedAt}`;
  a `409` on autosave returns `{error:{code}, latest:<post>}`.
- A check is `{id,state,reason,notes,notices,blocks}`; `reason` is `error|disconnected|unusable` when failed; `notices` are the
  server's one-sentence "block removed" lines; each block has `decision`, `needsAck`, `sources[{url,title,retrievedAt,verified}]`.
- `GET /api/blog/capabilities` returns `{writing,check,webSearch}`; image upload `POST .../assets` takes the raw file body and returns `{assetId,url}`.
- Public post returns `blocks[{anchor,markup,fallback,sources}]` where `anchor` is the index of the top-level node the block follows.

Still open on the page side: no cover-image control in Post settings (`saveSettings` accepts `coverAssetId`, nothing sends it), and the publish preview lists the proposed blocks without showing the post itself (original vs enriched, design-pages.md);
T037/T051/T064/T069 browser checks, T038 `internal/webui/dist` rebuild (Linux only), T043/T054/T058/T059/T066 tests for
preview wording, sources drawer, malicious markup refusal, reader interaction and list states, T061 reader library with
the actions removed (today every action is ignored), T062 polish, T070 Admin suspended note, T073 README gaps.

## Known environment traps

- No `go` on the Windows machine used so far; Docker Desktop was not running. Use Linux or WSL for Go.
- `vite build` fails on Windows on `main` (case clash `Artifacts.ts` / `Artifacts.tsx`). Build `internal/webui/dist`
  on Linux (T038 and the later rebuild tasks).

## Design gaps carried forward

Phone/Tablet/Landscape menus have no Blog entry; My posts and the Editor use a simplified rail; preview shows
the first two blocks only; interactions are drawn as static states. See `docs/design/README.md`.

- The public index (`listPublic`) asks for the first page only: the contract does not yet name a page parameter, so there is no "more" control until the backend defines one.

## Backend: plain posts (feat/blog-backend)

Done: migration `0026_blog.sql`, `internal/blog` (document allowlist that rebuilds what it keeps, slug rules, posts with
compare-and-set autosave and pinned versions, images, duplicate, publish original, unpublish, delete, the public reads
and the page metadata the server writes into `/b` and `/b/{slug}`), routes in `auth.BlogRoutes` / `auth.PublicBlogRoutes`,
and the kept tests (ownership, public surface, suspend, access, route inventory).

Behaviours worth knowing:
- Title and body are versioned; the summary, address and cover are settings and take effect at once, also on the
  published version (a settings save does not start a new version, so an open editor never hits a conflict from it).
- A published version is never rewritten: the next autosave starts a new one; readers see the pinned one until republish.
- `GET /api/blog/capabilities` reports `check` and `webSearch` false until the Hermes check exists; `publish` with
  `mode: enriched` answers 501 CAPABILITY_UNAVAILABLE.
- The public reads share one allowance of 600 a minute (not per caller); the page text copy in `/b/{slug}` is the title
  and summary, not the whole body.

Not done: everything that needs Hermes (T014, Phase 5 onward), T037 browser checks `blog-public` / `blog-editor`,
T072-T075.

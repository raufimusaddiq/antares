# Runbook — unsent drafts on the device

Issue #107 (server drafts), #172 (device policy). PRD §21.6, UXP-006/007.

Unsent composer text lives in two places, neither encrypted:

- **Server copy** (`chat_drafts`, `new_chat_drafts`; `web/src/DraftSync.tsx`): keyed by account and chat, owner-scoped, follows the
  account across devices, deleted with the chat or the account. Never in the search index or a push payload.
- **Device copy** (`localStorage`, `web/src/drafts.ts`): keyed `antares:draft:v1:<email>:<chat>`, so one account never reads another's.

## Device policy: purge on stale session (owner decision, #172)

| Moment | What leaves the device | Where |
|---|---|---|
| Sign-out | every draft and marker | `signOut` → `purgeDrafts` |
| Session expired when the app starts (401) | every draft | `loadSession` → `purgeDrafts` |
| Suspended when the app starts | every draft | `loadSession` → `purgeDrafts` |
| 401 or `ACCOUNT_SUSPENDED` on any chat API call during use | every draft | `chatApi.call` → `purgeDrafts` |
| A session loads for an account | other accounts' drafts; this account's drafts older than 7 days (`DRAFT_TTL_MS`) or unstamped | `loadSession` → `purgeStaleDrafts` |

Each device draft carries the time it was written (`antares:draft:v1:at:…`). A browser closed on an expired session runs no code,
so nothing can be purged at that moment; the next start either sees the 401 (everything goes) or a session for whoever signs in
(other accounts' text goes, and nothing older than a week stays). The server copy restores a purged draft after sign-in.

After any purge the device is **sealed**: `saveDraft` writes nothing until a session loads for an account again
(`purgeStaleDrafts`), so text typed into a composer still on screen after a 401 does not recreate the purged draft. The seal is
a `localStorage` key (`antares:drafts-sealed`), so every open tab obeys it, not only the tab that saw the 401 or the sign-out.

The 7 days are not a timer: nothing runs while the app is closed, so an old draft is cleared at the next start, not on its
seventh day. The product says exactly that in the Options drawer ("text older than 7 days is cleared the next time you open
Antares"). Tests: `web/src/DraftPolicy.test.ts` reads `localStorage` after every path above, including typing after a 401.

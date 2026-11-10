# Contract: check reply from Hermes

What the blog prompt asks Hermes to return, and what Antares accepts. **The run and its events are recorded
(`internal/hermes/testdata/blog_check_*`, research R3/R4); the envelope below is what the blog prompt asks for and is
still validated server-side, never trusted.** Anything not matching is
dropped, never repaired by guessing.

```json
{
  "version": 1,
  "notes": [
    { "kind": "clarity|fact|missing-context|unsourced", "text": "…", "afterNode": 3 }
  ],
  "blocks": [
    {
      "kind": "table|chart|metric|accordion|sources|…",
      "afterNode": 5,
      "markup": "root = Card([...])",
      "fallback": "Plain text version of the block",
      "sources": [ { "url": "https://…", "title": "…", "retrievedAt": "2026-11-08" } ]
    }
  ]
}
```

## Acceptance rules (all enforced server-side, in order)

1. Valid JSON, `version` known, total size under the limit; at most N notes and M blocks.
2. `afterNode` refers to a node that exists in the **checked version** (else the block is dropped).
3. `markup` parses as OpenUI Lang, uses only components and props in the **read-only reader allowlist**
   (research R6), stays within size and nesting limits, contains no raw HTML, script, iframe or unregistered
   action. Any violation drops that block and records a "rejected block" notice for the author.
4. A block whose markup shows data (table, chart, metric) MUST have at least one source; otherwise it is
   dropped and an `unsourced` note is added (FR-016).
5. Sources: `https` only, no credentials in the URL, no private/loopback host, `retrievedAt` is a date.
   `verified` is never set today: the run's events do not expose what a search returned (R4), so every source is
   unverified and its block needs the author's acknowledgement.
6. `notes` and `fallback` are plain text; they are escaped on display, never interpreted.
7. The envelope can never modify the author's document, the slug, the title, or any account data.

## Failure mapping

| Situation | Check state | Author sees |
|-----------|-------------|-------------|
| Hermes run failed / error | `failed` | "The check did not finish: the assistant reported an error." Retry / Publish original |
| Run timed out | `timed_out` | "…the assistant timed out. It may or may not have started." |
| Author cancelled | `cancelled` | "Cancelled before the check finished." |
| Connection lost | `failed` (disconnected) | "Connection to the assistant was lost." |
| Reply not parseable | `failed` | "The assistant's reply could not be used." Nothing is published |
| Some blocks rejected | `ready` | Preview with a notice per rejected block |
| Draft edited meanwhile | `ready` (spec 006: no longer `out_of_date`) | suggestions and blocks about a part that is gone show as no longer applicable |

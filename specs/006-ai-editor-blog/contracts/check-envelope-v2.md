# Check envelope v2 (what the author's assistant returns; prompt `blog-check v3`)

Extends specs/001-blog-cms-openui-publish/contracts/check-envelope.md. A reply is still ONE JSON object and nothing else, untrusted, refused as a whole when it is not the envelope, and every item that fails is dropped on its own with one sentence for the author.
`version` is `1` or `2`; a `1` reply has no `edits` and is accepted as before.

```json
{
  "version": 2,
  "notes":  [ { "kind": "clarity|fact|missing-context|unsourced|style", "text": "...", "afterNode": 3 } ],
  "blocks": [ { "...": "unchanged" } ],
  "edits":  [
    { "kind": "replace", "part": 2, "original": "an exact span of one line", "proposed": "the better wording", "reason": "one sentence" },
    { "kind": "insert",  "part": 4, "proposed": "A paragraph.\n\nAnother paragraph.", "reason": "one sentence" }
  ]
}
```

## Validation (server, per edit; a failing edit is dropped with a notice, the rest stay)
- `kind` is `replace` or `insert`; `part` is a top-level part of the draft that was read.
- `replace`: `original` is non-empty, at most 1,000 characters, has no newline, and is an exact substring of one line of that part with its `## ` or `- ` marker removed; `proposed` is non-empty, at most 4,000 characters, differs from `original`.
- `insert`: no `original`; `proposed` is non-empty, at most 8,000 characters, at most 12 paragraphs.
- `reason`: non-empty, at most 300 characters. All of `proposed`, `original`, `reason` are plain text (no control characters, no HTML tags, no Markdown link syntax, no URL or e-mail address).
- No figure that is not in the draft: every number in `proposed` (digits with their separators) must also appear in the draft's text (including the title), else the suggestion is dropped ("it contained a figure that is not in your draft"). Quoted spans of more than 6 words must be in the draft.
- At most `4 x parts + 8` edits; the rest are dropped with one notice. The order given is kept (most important first).
- `afterNode` of notes and blocks is validated as before.

## The prompt (blog-check v3), in outline
- You may propose changes; you must never change the draft yourself and never apply anything.
- Language: write notes, reasons and proposed text in the language of the draft (of the passage if the post mixes languages).
- Keep the author's voice and meaning; no new facts. If the draft is mostly a framework (headings and bullet points with little prose), propose `insert` suggestions with prose built only from the author's own points; use `blocks` for anything that needs a real source.
- About one suggestion per section, no more than the draft supports, most important first. If there is too little to edit, say so in one note and return no edits.
- The writing rules (spec 005) and the style-note paragraph are appended as before when the administrator has them on; the author's own words about tone win.

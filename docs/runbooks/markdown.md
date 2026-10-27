# Runbook — how replies are rendered (Markdown and OpenUI), measured

Issue #173. Replies are rendered by a small Markdown subset (`web/src/Markdown.tsx`) that never parses HTML and never
uses `dangerouslySetInnerHTML`; fenced `openui` blocks go to the OpenUI renderer (`web/src/openui/`) through
`splitSegments` (`MessageBody.tsx`). This page records where that differs from GitHub Flavored Markdown, on purpose.
`web/src/MarkdownGfm.test.tsx` holds one case per behaviour. The GFM shape is computed by micromark with its GFM
extension (a reference implementation, dev dependency only), not written by hand; the test fails if either shape changes,
if a difference is not listed here, or if a listed difference is no longer real.

## Same as GFM

Paragraphs, `*em*`, `**strong**`, `` `code` ``, `[text](https://…)` links, ordered and bullet lists, pipe tables
(alignment rows accepted), `$…$` left as literal text (GFM has no math either), and fenced code with a language (labelled, with a Copy button that copies the whole block,
however long).

## Intentional divergences

| Case | What we do | Why |
|---|---|---|
| `soft-line-break` | a single newline is a line break | model replies use newlines as breaks |
| `heading-levels` | `#` → h3 … `######` → h6 | a reply never outranks the page's own headings |
| `unsafe-link` | only absolute http(s) and mailto become links; anything else stays literal text | no `javascript:`, relative or data links |
| `raw-html` | shown as text | nothing in a reply is ever parsed as HTML |
| `nested-list` | flattened to one level | the subset has no nesting |
| `blockquote-paragraph` | text directly in the blockquote | the subset has no block nesting |
| `strikethrough` | `~~x~~` shown as written | not in the subset |
| `autolink` | bare URLs stay text | only explicit links are links |
| `task-list` | `[ ]` shown as text, never an input | a reply never creates form controls outside OpenUI |
| `setext-heading` | underlined headings are a paragraph | only `#` headings are recognised |

Inputs over 200,000 characters are not parsed at all and are shown as plain preformatted text.

## Safety that is tested, not assumed

- Script, event-handler attributes, `javascript:` links and raw HTML never reach the DOM as markup (`MarkdownDom.test.tsx`).
- OpenUI accepts only its component allowlist with size limits (`openui/library.tsx`, `validate.ts`; `openui.test.tsx`),
  and its only action is `reply`: it puts text in the composer and sends nothing. After a click the block says
  "Added to your message. Nothing was sent.", so a model-chosen label such as "Submit order" cannot suggest otherwise.
- Mixed Markdown with fenced OpenUI, long code lines and wide tables are in the browser check's rich conversation
  (`scripts/browser-check.mjs`), which fails on horizontal overflow at every phone width it measures.

# Blog check fixtures

Recorded 2026-11-09 from the deployed Hermes through Antares' own run stream, by `antares-smoke --events-out` on a
synthetic smoke account (`scripts/smoke_fixtures.py`; the account and its profile are removed afterwards). Prompt:
"Use web search to find the current population of Jakarta. Reply in two short sentences and give two source URLs you
actually used." Ids are replaced by stable fakes. Content is public information; nothing here is a user's.

- `blog_check_events.jsonl`: the events of one run that used web search twice (`{"event", "data"}` per line; the data is
  what Antares forwards to a browser: `tool.started` has `label` and `detail` (the search query); `tool.completed` has
  `summary`, `failed`, `durationMs`).
- `blog_check_output.txt`: the run's final output.

The `assistant.delta` lines are a fragment (the first two of several hundred, kept to show the shape): a real run streams the
whole reply in many deltas, so an accumulator must be written against `run.completed.output`, not this file's deltas.
`internal/hermes/blog_check_fixture_test.go` fails if the shape this recording shows (and research R4 rests on) changes.

What it shows (research R3, R4): see `specs/001-blog-cms-openui-publish/research.md`. Re-record after a Hermes upgrade.

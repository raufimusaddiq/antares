# Runbook — usage dashboard (issue #151, PRD §21.7)

`GET /api/me/usage?days=7|30|90` (Active guard) answers the caller's own finished replies since the start of the UTC
day `days-1` days ago: totals, by model, by chat (deleted chats as one "Deleted chats" row, from
`deleted_run_usage`) and by day (every day in the range, empty ones included). The account menu's **Usage** panel shows
it as a per-day bar chart with a table view, and tables by model and by chat; it reads only while open.

- Tokens are what Hermes reported. Replies that reported nothing are counted (`unreported`), never as zero tokens.
- The rupiah figure is this app's estimate from the dated price list (`internal/chat/usage.go`, `priceListDate`).
  A model with no known price is counted as `unpriced` and never priced; a group with nothing priceable shows no
  estimate. It is not the provider's bill: an admin reads the shared Kenari account's real usage at
  `/api/admin/kenari-usage` (#146).
- Ownership is in the query (`store.UsageRows`: the caller's own runs joined to the caller's own chats).
  `TestUsageRangeIsOwnerScopedAndHonest` covers another user's usage, the 30/90-day bounds, deleted chats, unreported
  and unpriced replies.

# Runbook — per-chat message queue

Issue #110, PRD §27.4 (`QUEUE-001..004`), §26.1 (message queue row), §29.1 (queue endpoint), `LLM-ACC-010`. Part of #162
(composer intents: Send, Steer, Queue). Phase 0 is still FAIL: this is trusted-user operation, not a multi-user security claim.

## What it is

While a reply is running, the composer offers **Queue** beside **Steer** and the disabled **Send**. A queued message is a
normal new turn sent after the reply ends. It is never steer: steer is guidance into the running reply (`turn-controls.md`).

The queue lives in Antares' SQLite (`internal/store/migrations/0018_message_queue.sql`), keyed by owner **and** chat, so a
refresh or another device shows the same queue. The browser holds no queue of its own; every change answers with the queue as
stored. Limit: `store.MaxQueuedPerChat` (20) per chat, each message at most the normal message size. Queued messages are
text only; images are not queued. Rows cascade with the chat and the user.

## Routes

| Route | Does |
|---|---|
| `GET /api/chats/{chatId}/queue` | `{items:[{id, position, input, status}], paused}`; `position` is 1-based; `status` is `QUEUED` or `SUBMITTING` |
| `POST /api/chats/{chatId}/queue` `{input}` | append; `409 QUEUE_FULL` past the limit. A free chat with an unpaused queue sends its head at once |
| `PATCH /api/chats/{chatId}/queue/{itemId}` `{input}` | edit a `QUEUED` message (one being sent cannot change: `404`) |
| `DELETE /api/chats/{chatId}/queue/{itemId}` | remove a `QUEUED` message |
| `PATCH /api/chats/{chatId}/queue` `{paused}` | pause (`USER`) or resume. Resuming is the explicit recovery: a free chat sends its head at once |
| `POST /api/chats/{chatId}/queue/next` | **Send next**: send the head now, even from a paused queue (it stays paused). A busy chat is `409 RUN_CONFLICT`, an empty queue `409 QUEUE_EMPTY` |

`POST /queue` and `PATCH /queue` answer with `started: {runId, input}` when their dispatch began a run, so the browser follows
that reply instead of showing an idle chat.

The last two are additions to the PRD §29.1 table, which lists only the collection and item routes: pause/unpause and "Send
next" are required by `QUEUE-001` and need a route. Every route checks ownership before reading the body; a foreign chat or item
is `404`, byte-identical to one that does not exist (`threat_test.go` probes all six).

## Exactly once (`QUEUE-003`)

1. **Claim.** `store.ClaimNext` moves the head from `QUEUED` to `SUBMITTING` with a compare-and-set inside a transaction, and
   refuses while another item of the same chat is `SUBMITTING`. Of any number of concurrent dispatchers (two tabs, a tab and the
   run-end hook) exactly one gets it (`TestClaimNextIsExactlyOnce`, `TestConcurrentSendNextDispatchesOnce`).
2. **Admit.** The claimed message goes through `Service.admit`, the same path as a typed message: ownership, gate (approval,
   binding), model policy, per-user run cap, token budget, a free chat. Its idempotency key is `queue-<item id>`, stable for the
   item's life, so a repeated dispatch replays the run it already became instead of sending again.
3. **Settle.** Admitted: the row is deleted. Refused: it goes back to the head, and the queue is paused with the refusal's code
   (except `RUN_CONFLICT`: a busy chat's own end dispatches again).
4. **No stranding.** A run can end between a busy-chat refusal and the release of that claim; its end-of-run dispatch then finds
   the claim in flight and stands down. So after releasing a busy-chat refusal the dispatcher reads the chat once more and, if it
   is now free, dispatches again (`TestQueueIsNotStrandedWhenTheRunEndsDuringARefusedClaim`).

A process that dies holding a claim: on start-up `store.RecoverRuns` puts it back to `QUEUED` and pauses the chat's queue
`INTERRUPTED`; the owner decides, and a re-send under the same key replays any run it had become.

## When it sends on its own, and when it stops

- A run that ends `COMPLETED` sends the next message, **before** `run.completed` is announced, so a browser that reads the chat on
  that event already sees the new run and follows it.
- `FAILED`, `TIMED_OUT`, `CANCELLED` (including the user's own cancel) and `DISCONNECTED` pause a queue that has messages waiting,
  with that status as the reason. Suspension, a missing binding, a reached budget or an unavailable assistant at dispatch pause it
  with that code. The first reason stays until the owner resumes. Nothing is retried blindly.

## Not built here

- Images in queued messages.
- Enqueueing the text of a steer that arrived too late (`QUEUE-004`): the composer says "too late" and keeps the text, and the
  user can send or queue it themselves; nothing is enqueued without that choice.
- Reordering (only edit and remove).

# Runbook — push on run completion (UXP-002, issue #59)

Code: `internal/push` (VAPID, RFC 8291 encryption, sender, routes, notifier), `internal/store/push.go`
(owner-scoped queries), `web/public/sw.js` (the worker), `web/src/push.ts` and `Notifications.tsx` (opt-in UI).
PRD §21.2, §22, §23.

## What a notification can say

`push.Payload` is `{v, kind, chatId}`: `kind` is `completed` or `failed`, `chatId` is the opaque app chat id
used only for the link. There is no field for a title, prompt, message or output, and the service worker
never renders the payload: its text is fixed per `kind`. Tests: `TestPayloadIsAClosedShape`,
`TestPayloadCarriesNoConversationContent` (decrypts what a push service receives), `web/src/push.test.ts`
(runs `sw.js`). The body is encrypted (aes128gcm) end to end to the browser, so the push service sees none of it either.

## When one is sent

Only from a **stored** terminal status (`finish` in `internal/chat/service.go`): `COMPLETED` → `completed`,
`FAILED`/`TIMED_OUT` → `failed`. `CANCELLED` and `DISCONNECTED` never notify. The sender re-checks that the
account is APPROVED with a READY, verified binding, then the user's mode (`off` by default; `all`;
`failures`), then each device. One claim row per `{device, run, kind}` is written before the send and **kept** whatever happens, so
the notification is sent at most once per device and run: a failed send is retried only inside the same
attempt (network, 429, 5xx: 3 tries), never when a terminal status is reported again. `delivered_at` is
set only when the push service accepted it, so a claim without it is a notification that was not delivered
(also what a crash mid-send leaves). 404/410 delete the device. Delivery is best effort, not guaranteed.
Delivery records older than 30 days are pruned on each notification.

## Opt-in and revoke

Nothing is requested until the reader presses "Turn on for this device" (that click asks for browser
permission, registers `/sw.js`, subscribes, and stores the device; the first device sets mode `all`).
"Turn off on this device" deletes the server row first, so delivery stops even if the browser call fails.
Signing out revokes this browser's device first (best effort), so a shared browser does not keep the
previous account's alerts. An endpoint registered to another account is refused (`409 ENDPOINT_IN_USE`) and the holder keeps it: presenting an endpoint never revokes someone else's device. The panel then drops that browser subscription and registers a fresh one. The same account re-registering an endpoint replaces its own row.
Mute from the same panel (`off`) keeps devices but sends nothing, and adding a device does not undo it (the panel says so).

## Deep link

The click opens `/chat/<chatId>` (the worker only accepts a 32-hex id). The shell is the same for everyone;
the page then asks `GET /api/chats/<id>`, which answers its owner only. Anyone else gets `404 NOT_FOUND`, sees
"not available to this account", and no history is requested. Signed out, the id is kept in `sessionStorage`
across the Google round trip. Tests: `TestDeepLinkTargetOpensForTheOwnerOnly`, `web/src/DeepLink.test.tsx`.

## Storage and egress

`push_subscriptions` seals the endpoint and keys with the secret box, bound to the row id; only a SHA-256 of
the endpoint is kept in clear. Endpoints must be https on 443 at a known push service
(`internal/push/webpush.go` `pushHosts`), and the egress client refuses non-public addresses and redirects.

## Configuration

`ANTARES_VAPID_PRIVATE_KEY` (base64url of a raw 32-byte P-256 scalar) and `ANTARES_VAPID_SUBJECT`
(`mailto:` or `https://`): both or neither. Neither → the UI says notifications are not set up, the routes
answer `FEATURE_NOT_CONFIGURED`, nothing is sent. Generate once: `head -c32 /dev/urandom | basenc --base64url | tr -d =`.
Changing the key invalidates every stored subscription (browsers bind to the public key).

## Not done

Purging devices when an account is suspended (they are skipped while ineligible and work again on
reactivation); a notification for `unknown` run state; per-chat mute.

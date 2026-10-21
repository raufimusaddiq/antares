# Runbook — security invariants

These are the rules that make Antares safe to run for more than one person. They are not features
and they are not negotiable. A PR that weakens one of them is rejected regardless of what it
delivers.

## 1. Default deny

No approved account with a healthy tenant binding means **no Hermes request**. Every Hermes call
passes through one gate that checks, in this order:

1. Is the session valid?
2. Is the account approved (not pending, not rejected, not suspended)?
3. Is a Hermes profile bound to this account?
4. Is that binding verified healthy?

Any "no" returns an error to the client. There is no path that skips the gate, and no cached
response that substitutes for it.

## 2. Identity and authorization are separate

Google authenticates **who you are**. Antares decides **what you may do**. A successful Google
sign-in creates a `pending` account that can do nothing but see its own status. Approval is an
explicit admin action recorded in the audit log.

## 3. The backend selects the profile

A user never supplies a profile name, route prefix, API key, header or session id. If a request
carries one, it is rejected — not ignored, not overridden. This is the single most important rule in
the product: a user-supplied profile name is a cross-tenant read waiting to happen.

## 4. Ownership on every query

Every database query touching user resources enforces ownership in the query itself, not in the
handler above it. A handler that fetches a row and then checks the owner is a handler that will one
day forget.

Negative tests with two test users (A and B) are part of the definition of done for any change that
touches user data. A test that only exercises the happy path proves nothing about isolation.

## 5. Memory isolation is a release blocker

Per-user memory requires a per-user Hermes profile. Profiles are **not sandboxes** — the PRD's
threat model (§5.5) says so plainly. Until the tool filesystem isolation test passes, restrict the
toolset to tools that cannot cross the boundary, or block multi-user access.

**Never claim secure multi-user readiness before that test passes.**

## 6. Admin is blind by default

An admin can approve, reject, suspend, revoke and see account status and audit metadata. An admin
**cannot** read a user's conversations, memories, files or tool outcomes. If an admin needs to see
content for a support case, that is a separate, logged, user-consented mechanism — not a default
capability.

## 7. Secrets never leak

- Hermes API keys, Google client secrets, session signing keys and push keys come from runtime
  secrets.
- No credential in a log line, an error string, a read model, a URL or a commit.
- Error messages describe the failure, not the credential. A refusal reason that interpolates a
  secret is a leak.
- Test fixtures are sanitized: real shapes, fabricated values.

## 8. Failure is visible

A failed, cancelled, timed-out or disconnected run is never shown as completed. A capability that is
not configured is `unsupported`, not silently missing. The UI's job is to tell the truth about the
backend, including when the backend is broken.

## 9. OpenUI is data, not code

Generative UI markup from the model is rendered through a registered component allowlist. Actions
route back through the secured Go API. Model output is never executed, and never reaches a DOM sink
directly.

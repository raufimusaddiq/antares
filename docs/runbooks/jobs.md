# Runbook — scheduled tasks (PRD §12, issue #105)

## Switched off by default

`ANTARES_JOBS=on` in `/opt/antares/antares.env` mounts the routes and reports the `jobs` capability, which shows the
menu panel. Off (the default), the routes stay the 501 placeholder and the capability says it is switched off
(PRD §12 rule 6). It is turned on only after the owner-isolation and suspension probe against the deployed Hermes
passes and is recorded in `docs/hermes/` (the fake-upstream tests here prove the app's side, not Hermes'). Suspension
pauses any jobs an account has whether or not the switch is on.

## What exists

The account menu's **Scheduled tasks** panel lists, adds, pauses, resumes, runs now and deletes the caller's own
tasks. Routes (Active guard, same origin for changes): `GET/POST /api/jobs`, `GET/DELETE /api/jobs/{jobId}`,
`POST /api/jobs/{jobId}/pause|resume|run`. They proxy Hermes `GET/POST /api/jobs`, `GET/DELETE /api/jobs/{id}` and
`POST /api/jobs/{id}/pause|resume|run` (`gateway/platforms/api_server.py`, v0.22.5).

## Why one user cannot reach another's task

Hermes keeps each profile's jobs in that profile's own store and runs them under that profile (`cron/jobs.py`,
"Cron is per-profile by design"). Antares sends every call to `/p/<caller's profile>` with the caller's own key
(`chat.Service.gate`), so another account's job id is `Job not found` there and Antares answers 404. A job id is
checked against Hermes' shape (`^[a-f0-9]{12}$`) before any call. `TestJobsAreOwnerScoped` tries every job route
with another user's id.

## Limits

- Antares builds the request body itself: name, schedule, instructions, `deliver: "local"` (no messaging channel,
  so no global delivery channel). A request carrying any other field is a 400.
- At most 10 tasks per account (`JOB_LIMIT`).
- A manual "run now" counts against the hourly bound: once per task per hour (429 `RATE_LIMITED`).
- At most once an hour: a single time, an interval of 60 minutes or more, or a five-field cron expression with one
  fixed minute. A task is created **paused**, its parsed schedule is checked, and it is resumed only if it passes;
  otherwise it is deleted (`SCHEDULE_TOO_FREQUENT`). Changing a schedule means deleting the task and adding it again.
- Tools: a task's agent turn gets the profile's `agent.disabled_toolsets` on top of the cron defaults
  (`cron/scheduler.py _resolve_cron_disabled_toolsets`, v0.22.5), so it has the same tools as the user's chats.
- Not limited by Antares: a task's maximum runtime (Hermes' own cron settings).

## Suspension (PRD §12 rule 3)

Suspending an account pauses every enabled task in its workspace through its own binding, then reads the list back. Every job change holds a per-account lock and re-checks the account after taking it, and the suspension takes the same lock, so a change racing a suspension either finishes first (and is then paused) or is refused; the 10-task count and the create are atomic under it too. Retries rotate through the marked accounts, 20 per tick.
The outcome is audited as `admin.suspend_jobs` (`ok` or `pending`). The suspension itself records
`SUSPENSION_PENDING_JOB_SHUTDOWN: …` as the account's tenant detail in the same transaction, and it is cleared only
once the pause is verified. So if Hermes cannot be reached, or the process stops before the pause runs, the admin user
screen shows it and the chat service retries every 5 minutes; a retry that succeeds clears it and audits
`system.suspend_jobs ok` (`TestSuspensionWithoutAPauseIsRecoveredByTheRetry`). Reactivating
leaves the tasks paused: the user resumes them. An account still marked pending cannot be reactivated (409) until a
retry verifies its jobs paused, so the retry never loses it (`TestReactivationWaitsForTheJobShutdown`). Tests: `TestSuspensionPausesTheUsersJobsUpstream`,
`TestSuspensionWithUnreachableJobsIsPendingThenRetried`.

## Not here

- Task results: Hermes stores a run's output in the profile and exposes no route to read it back, so the panel
  shows when a task last ran and its status, not what it produced.
- Editing a task: delete and add it again (every schedule then passes the creation check).

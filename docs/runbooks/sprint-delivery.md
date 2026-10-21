# Runbook — sprint delivery

The sequence for every ticket in this repository. The PRD is the specification; this is how work
gets from a ticket to `main`.

## 1. Before you start

- The ticket exists and names the PRD sections it implements.
- You have read `AGENTS.md` and `docs/runbooks/security-invariants.md`.
- If the ticket touches Hermes integration, Phase 0 has passed. If it has not, stop — that is the
  gate, not a formality.

## 2. Branch and worktree

```bash
git fetch origin
git worktree add /home/ubuntu/antares-wt/s<N>-<slug> -b sprint/<N>-<slug> origin/main
```

One ticket, one worktree, one branch. Never work in the main checkout, and never share a worktree
between two tickets.

## 3. Build

- Go: `go build ./...` and `go vet ./...` must pass.
- Frontend: the React bundle builds into the Go static assets.
- Migrations are forward-only and numbered. A migration that has been applied is never edited.
- Run the tests you touched, plus the package tests. CI runs the full suite — do not run a full
  `-race ./...` locally as a gate; it is slow and CI already does it.

## 4. Pull request

- PR to protected `main`, merge commit, never squash.
- The body states: what changed, which PRD sections it implements, what you verified and how, and
  what remains open.
- If the change touches user data, the PR body names the ownership tests that prove isolation.

## 5. Review rounds

- The review bot publishes a verdict per commit. **Batch every fix into one commit per round.**
- After pushing a fix, wait for the verdict on the new head. An approval on the previous head
  describes code that no longer exists.
- If the PR goes behind `main`: `git fetch origin && git merge origin/main`, resolve keeping both
  sides, one commit.

## 6. Merge and clean up

The lead merges. After the merge, **you** clean up before the task is called done:

```bash
git fetch origin
git switch main && git pull --ff-only
git worktree remove ../antares-wt/s<N>-<slug>
git branch -d sprint/<N>-<slug>
git worktree prune
```

Never force-remove a worktree with uncommitted or unpushed work. If the branch has work you cannot
account for, stop and report it.

## 7. Definition of done

A ticket is done when all of these hold, not when the PR is open:

- Merged to `main`.
- Tests that fail before the change and pass after it exist and are committed.
- The PRD section it implements is satisfied, or the gap is written down with a reason.
- Worktree and branch removed.
- The ticket commented with the merge SHA and closed.

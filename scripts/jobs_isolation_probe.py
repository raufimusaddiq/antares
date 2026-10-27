"""Live owner-isolation and suspension probe for scheduled tasks (#105, PRD §12 rule 6).

Run by scripts/smoke_fixtures.py, which gives this process ANTARES_SMOKE_COOKIE_ADMIN|A|B|PENDING for disposable
synthetic accounts and removes them (and their Hermes profiles, with their jobs) afterwards, pass or fail.

Usage: jobs_isolation_probe.py <origin> <antares.sqlite> <report.json>

Hermes is read inside its container (HERMES_CONTAINER, default "hermes", as scripts/hermes-route-probe.sh) as the
hermes user, read-only: the job's output file and the profile's job store. Prints no cookie, key or prompt output beyond the marker.
"""
import json
from contextlib import closing
import os
import sqlite3
import subprocess
import sys
import time
import urllib.error
import urllib.request

COOKIE = "__Host-antares_session"
MARKER = "NO_TERMINAL_TOOL"
RESPONSE_HEADING = "## Response\n\n"  # Hermes' run document (cron/scheduler_prompt.py _RESPONSE_HEADING)
PROMPT = ("Use your terminal tool to run the command id and report its output. "
          f"If you have no terminal tool, reply with exactly {MARKER} and nothing else.")


class Probe:
    def __init__(self, origin, db, cookies, hermes_cmd, wait=600, run_wait=300, poll=5):
        self.origin, self.db, self.cookies, self.hermes_cmd = origin, db, cookies, hermes_cmd
        self.wait, self.run_wait, self.poll = wait, run_wait, poll
        self.results = []

    def call(self, who, method, path, body=None):
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(self.origin + path, data=data, method=method)
        req.add_header("Cookie", f"{COOKIE}={self.cookies[who]}")
        req.add_header("Accept", "application/json")
        req.add_header("User-Agent", "antares-jobs-probe/1")  # the edge refuses Python's default agent
        if method != "GET":
            req.add_header("Origin", self.origin)  # every mutation is same-origin checked before ownership
        if data is not None:
            req.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(req, timeout=60) as r:
                status, raw = r.status, r.read()
        except urllib.error.HTTPError as e:
            status, raw = e.code, e.read()
        except OSError:  # refused, unreachable, reset or timed out (URLError and socket timeouts are OSErrors)
            return 0, None  # no HTTP answer at all: the caller's check fails, and the report is still written
        try:
            return status, (json.loads(raw) if raw else None)
        except ValueError:  # not JSON: the caller sees no body, and its check fails
            return status, None

    def get(self, who, path):
        """A GET whose body is a JSON object, or {} for any other answer (empty, not JSON, a list): a surprise in the
        deployment becomes a failed check in the report, never a crash before the report is written."""
        st, body = self.call(who, "GET", path)
        return st, body if isinstance(body, dict) else {}

    def hermes_read(self, shell):
        """Runs a read-only shell command in the Hermes container and returns its stdout, or "" on failure or timeout."""
        try:
            out = subprocess.run(self.hermes_cmd + ["sh", "-c", shell], capture_output=True, text=True, timeout=60)
        except subprocess.TimeoutExpired:
            return ""
        return out.stdout if out.returncode == 0 else ""

    def check(self, name, ok, detail=""):
        self.results.append({"check": name, "pass": bool(ok), "detail": detail})
        print(("PASS " if ok else "FAIL ") + name + (f" — {detail}" if detail else ""), flush=True)
        return ok

    def until(self, cond, seconds):
        deadline = time.time() + seconds
        while not cond() and time.time() < deadline:
            time.sleep(self.poll)
        return cond()

    def run(self):
        # Fails fast when Antares does not answer at all, rather than waiting out the admin poll below.
        if not self.check("Antares answers", self.call("ADMIN", "GET", "/api/me")[0] != 0):
            return
        if not self.check("admin API reachable (admin workspace ready)",
                          self.until(lambda: self.call("ADMIN", "GET", "/api/admin/users")[0] == 200, self.wait)):
            return
        me = {w: self.get(w, "/api/me")[1] for w in ("A", "B")}
        if not self.check("A and B are signed in", all(isinstance(me[w].get("id"), str) and me[w]["id"] for w in ("A", "B"))):
            return
        for w in ("A", "B"):
            if not me[w].get("canChat"):
                st, _ = self.call("ADMIN", "POST", f"/api/admin/users/{me[w]['id']}/approve", {})
                self.check(f"admin approves {w}", st == 200, str(st))
        if not self.check("A and B provisioned and able to chat",
                          self.until(lambda: all(self.get(w, "/api/me")[1].get("canChat") is True for w in ("A", "B")), self.wait)):
            return

        _, caps = self.get("A", "/api/capabilities")
        cap = next((c for c in caps.get("capabilities") or [] if isinstance(c, dict) and c.get("id") == "jobs"), None)
        self.check("the jobs capability is available", cap is not None and cap.get("status") == "available", json.dumps(cap))

        st, job = self.call("A", "POST", "/api/jobs", {"name": "isolation probe", "schedule": "every 2h", "prompt": PROMPT})
        job = job if isinstance(job, dict) else {}
        if not self.check("A creates a job", st == 201 and isinstance(job.get("id"), str) and job["id"] != "", str(st)):
            return
        jid = job["id"]
        self.check("the new job is scheduled, not paused", job.get("paused") is False,
                   json.dumps({k: job.get(k) for k in ("state", "paused", "schedule")}))

        st, lb = self.get("B", "/api/jobs")
        listed = lb.get("jobs")
        self.check("B's list does not contain A's job",
                   st == 200 and isinstance(listed, list) and all(not isinstance(j, dict) or j.get("id") != jid for j in listed),
                   f"{st} {len(listed) if isinstance(listed, list) else 'no'} jobs")
        for method, path in [("GET", f"/api/jobs/{jid}"), ("DELETE", f"/api/jobs/{jid}"), ("POST", f"/api/jobs/{jid}/pause"),
                             ("POST", f"/api/jobs/{jid}/resume"), ("POST", f"/api/jobs/{jid}/run")]:
            st, _ = self.call("B", method, path, {} if method == "POST" else None)
            self.check(f"B {method} {path.replace(jid, '<A job>')} is 404", st == 404, str(st))
        st, still = self.get("A", f"/api/jobs/{jid}")
        self.check("A's job is unchanged after B's attempts",
                   st == 200 and still.get("paused") is False and still.get("name") == "isolation probe", str(st))

        # One real execution, asked to use the terminal. What this shows is the run's own answer: the agent reported no
        # terminal tool, and its output holds no command result. It does not read the toolset Hermes granted the run.
        st, _ = self.call("A", "POST", f"/api/jobs/{jid}/run", {})
        self.check("A runs the job once", st == 200, str(st))
        last = {}

        def finished():
            nonlocal last
            last = self.get("A", f"/api/jobs/{jid}")[1]
            return bool(last.get("lastRunAt") and last.get("lastStatus"))

        self.check("the run finished", self.until(finished, self.run_wait),
                   json.dumps({k: last.get(k) for k in ("lastRunAt", "lastStatus", "state")}))
        with closing(sqlite3.connect(f"file:{self.db}?mode=ro", uri=True)) as db:
            row = db.execute("SELECT profile_name FROM tenant_bindings WHERE user_id = ?", (me["A"]["id"],)).fetchone()
        if not self.check("A has a workspace binding", row is not None and isinstance(row[0], str) and row[0] != ""):
            return
        profile = row[0]
        # The run document repeats the prompt (which names the marker) before "## Response": only the response counts.
        doc = self.hermes_read(f"cat /opt/data/profiles/{profile}/cron/output/{jid}/*.md 2>/dev/null")
        reply = doc.rsplit(RESPONSE_HEADING, 1)[1] if RESPONSE_HEADING in doc else ""
        # Asked to reply with exactly the marker when it has no terminal: the reply must be the marker and nothing else
        # (quotes, backticks and a full stop around it allowed), so any command output beside it fails the check.
        said = reply.strip().strip("`*_\"'. \n")
        self.check("the cron run's reply is exactly the no-terminal marker", said == MARKER,
                   "exact marker" if said == MARKER else (f"reply of {len(said)} characters" if said else "no reply section read"))

        # Suspension pauses the job upstream and verifies it.
        st, view = self.call("ADMIN", "POST", f"/api/admin/users/{me['A']['id']}/suspend", {"reason": "jobs isolation probe"})
        view = view if isinstance(view, dict) else None
        self.check("admin suspends A", st == 200, str(st))
        self.check("suspension verified the jobs paused (no pending mark)", view is not None and view.get("tenantDetail") == "",
                   (view or {}).get("tenantDetail", "")[:60])
        with closing(sqlite3.connect(f"file:{self.db}?mode=ro", uri=True)) as db:
            row = db.execute(
                "SELECT outcome FROM audit_log WHERE action = 'admin.suspend_jobs' AND target_id = ? ORDER BY created_at DESC LIMIT 1",
                (me["A"]["id"],)).fetchone()
        self.check("audit admin.suspend_jobs ok", row is not None and row[0] == "ok", str(row))
        upstream = []
        try:
            store = json.loads(self.hermes_read(f"cat /opt/data/profiles/{profile}/cron/jobs.json"))
            jobs = store.get("jobs", []) if isinstance(store, dict) else store
            upstream = [j for j in jobs if isinstance(j, dict) and j.get("id") == jid]
        except (ValueError, AttributeError):
            pass
        self.check("Hermes' own store has the job disabled", len(upstream) == 1 and upstream[0].get("enabled") is False,
                   json.dumps({k: upstream[0].get(k) for k in ("enabled", "state")}) if upstream else "job not found in store")

    def report(self):
        return {"passed": bool(self.results) and all(r["pass"] for r in self.results), "checks": self.results,
                "at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}


def main(argv, env):
    if len(argv) != 4:
        print("usage: jobs_isolation_probe.py <origin> <antares.sqlite> <report.json>", file=sys.stderr)
        return 3
    origin, db, report = argv[1:]
    cookies = {w: env.get("ANTARES_SMOKE_COOKIE_" + w, "") for w in ("ADMIN", "A", "B")}
    if not all(cookies.values()):
        print("ANTARES_SMOKE_COOKIE_ADMIN, _A and _B are required (run it through scripts/smoke_fixtures.py)", file=sys.stderr)
        return 3
    hermes_cmd = ["docker", "exec", "-u", "hermes", env.get("HERMES_CONTAINER", "hermes")]
    p = Probe(origin, db, cookies, hermes_cmd)
    p.run()
    rep = p.report()
    with open(report, "w") as f:
        json.dump(rep, f, indent=2)
        f.write("\n")
    return 0 if rep["passed"] else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv, os.environ))

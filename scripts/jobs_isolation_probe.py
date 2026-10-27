"""Live owner-isolation and suspension probe for scheduled tasks (#105, PRD §12 rule 6).

Run by scripts/smoke_fixtures.py, which gives this process ANTARES_SMOKE_COOKIE_ADMIN|A|B|PENDING for disposable
synthetic accounts and removes them (and their Hermes profiles, with their jobs) afterwards, pass or fail.

Usage: jobs_isolation_probe.py <origin> <antares.sqlite> <report.json>

Hermes is read inside its container (HERMES_CONTAINER, default "hermes", as scripts/hermes-route-probe.sh) as the
hermes user, read-only: the job's output file and the profile's job store. Prints no cookie, key or prompt output beyond the marker.
"""
import json
import os
import sqlite3
import subprocess
import sys
import time
import urllib.error
import urllib.request

COOKIE = "__Host-antares_session"
MARKER = "NO_TERMINAL_TOOL"
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
                raw = r.read()
                return r.status, (json.loads(raw) if raw else None)
        except urllib.error.HTTPError as e:
            raw = e.read()
            try:
                return e.code, json.loads(raw)
            except ValueError:
                return e.code, None

    def hermes_read(self, shell):
        """Runs a read-only shell command in the Hermes container; its stdout, "" on any failure."""
        out = subprocess.run(self.hermes_cmd + ["sh", "-c", shell], capture_output=True, text=True)
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
        if not self.check("admin API reachable (admin workspace ready)",
                          self.until(lambda: self.call("ADMIN", "GET", "/api/admin/users")[0] == 200, self.wait)):
            return
        me = {w: self.call(w, "GET", "/api/me")[1] for w in ("A", "B")}
        for w in ("A", "B"):
            if not me[w]["canChat"]:
                st, _ = self.call("ADMIN", "POST", f"/api/admin/users/{me[w]['id']}/approve", {})
                self.check(f"admin approves {w}", st == 200, str(st))
        if not self.check("A and B provisioned and able to chat",
                          self.until(lambda: all(self.call(w, "GET", "/api/me")[1]["canChat"] for w in ("A", "B")), self.wait)):
            return

        _, caps = self.call("A", "GET", "/api/capabilities")
        cap = next((c for c in (caps or {}).get("capabilities", []) if c.get("id") == "jobs"), None)
        self.check("the jobs capability is available", cap is not None and cap.get("status") == "available", json.dumps(cap))

        st, job = self.call("A", "POST", "/api/jobs", {"name": "isolation probe", "schedule": "every 2h", "prompt": PROMPT})
        if not self.check("A creates a job", st == 201 and job and job.get("id"), str(st)):
            return
        jid = job["id"]
        self.check("the new job is scheduled, not paused", job.get("paused") is False,
                   json.dumps({k: job.get(k) for k in ("state", "paused", "schedule")}))

        st, lb = self.call("B", "GET", "/api/jobs")
        self.check("B's list does not contain A's job", st == 200 and all(j["id"] != jid for j in lb["jobs"]),
                   f"{st} {len((lb or {}).get('jobs', []))} jobs")
        for method, path in [("GET", f"/api/jobs/{jid}"), ("DELETE", f"/api/jobs/{jid}"), ("POST", f"/api/jobs/{jid}/pause"),
                             ("POST", f"/api/jobs/{jid}/resume"), ("POST", f"/api/jobs/{jid}/run")]:
            st, _ = self.call("B", method, path, {} if method == "POST" else None)
            self.check(f"B {method} {path.replace(jid, '<A job>')} is 404", st == 404, str(st))
        st, still = self.call("A", "GET", f"/api/jobs/{jid}")
        self.check("A's job is unchanged after B's attempts",
                   st == 200 and still.get("paused") is False and still.get("name") == "isolation probe", str(st))

        # One real execution, asked to use the terminal. What this shows is the run's own answer: the agent reported no
        # terminal tool, and its output holds no command result. It does not read the toolset Hermes granted the run.
        st, _ = self.call("A", "POST", f"/api/jobs/{jid}/run", {})
        self.check("A runs the job once", st == 200, str(st))
        last = {}

        def finished():
            nonlocal last
            last = self.call("A", "GET", f"/api/jobs/{jid}")[1] or {}
            return bool(last.get("lastRunAt") and last.get("lastStatus"))

        self.check("the run finished", self.until(finished, self.run_wait),
                   json.dumps({k: last.get(k) for k in ("lastRunAt", "lastStatus", "state")}))
        profile = sqlite3.connect(f"file:{self.db}?mode=ro", uri=True).execute(
            "SELECT profile_name FROM tenant_bindings WHERE user_id = ?", (me["A"]["id"],)).fetchone()[0]
        out = self.hermes_read(f"cat /opt/data/profiles/{profile}/cron/output/{jid}/*.md 2>/dev/null | tail -c 4000")
        self.check("the cron run reported no terminal tool and shows no command output", MARKER in out and "uid=" not in out,
                   "marker found" if MARKER in out else ("uid= present" if "uid=" in out else "no output read"))

        # Suspension pauses the job upstream and verifies it.
        st, view = self.call("ADMIN", "POST", f"/api/admin/users/{me['A']['id']}/suspend", {"reason": "jobs isolation probe"})
        self.check("admin suspends A", st == 200, str(st))
        self.check("suspension verified the jobs paused (no pending mark)", bool(view) and view.get("tenantDetail", "") == "",
                   (view or {}).get("tenantDetail", "")[:60])
        row = sqlite3.connect(f"file:{self.db}?mode=ro", uri=True).execute(
            "SELECT outcome FROM audit_log WHERE action = 'admin.suspend_jobs' AND target_id = ? ORDER BY created_at DESC LIMIT 1",
            (me["A"]["id"],)).fetchone()
        self.check("audit admin.suspend_jobs ok", row is not None and row[0] == "ok", str(row))
        upstream = []
        try:
            store = json.loads(self.hermes_read(f"cat /opt/data/profiles/{profile}/cron/jobs.json"))
            jobs = store.get("jobs", []) if isinstance(store, dict) else store
            upstream = [j for j in jobs if j.get("id") == jid]
        except ValueError:
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

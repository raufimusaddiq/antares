"""Offline tests for jobs_isolation_probe.py (#105): a fake Antares (HTTP) and a fake Hermes container (a shell command
over a temporary directory) stand in for production, so the probe's verdict logic is checked without either."""
import http.server
import json
import os
import shutil
import sqlite3
import sys
import tempfile
import threading
import unittest

import jobs_isolation_probe as probe

JOB = "0123456789ab"


class FakeAntares(http.server.BaseHTTPRequestHandler):
    """Just enough of Antares for the probe. `leak` lets B read A's job; `stuck` leaves the job enabled upstream."""
    leak = False
    stuck = False
    garbled = False  # B's job list answers something that is not JSON
    state = {}

    def log_message(self, *a):
        pass

    def who(self):
        c = self.headers.get("Cookie", "")
        return c.split("=", 1)[1] if "=" in c else ""

    def send(self, code, body=None):
        raw = b"" if body is None else json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self):
        st, me, p = self.state, self.who(), self.path
        if p == "/api/admin/users":
            return self.send(200 if me == "admin" else 403, {"users": []})
        if p == "/api/me":
            return self.send(200, {"id": me, "canChat": st["approved"].get(me, False)})
        if p == "/api/capabilities":
            return self.send(200, {"capabilities": [{"id": "jobs", "status": "available"}]})
        if p == "/api/jobs":
            if me == "b" and self.garbled:
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b"<html>oops</html>")
                return
            return self.send(200, {"jobs": [st["job"]] if me == "a" and st.get("job") else []})
        if p == f"/api/jobs/{JOB}":
            if me == "a" or self.leak:
                return self.send(200, st["job"])
            return self.send(404, {"error": {"code": "NOT_FOUND"}})
        return self.send(404)

    def do_DELETE(self):
        return self.send(404 if self.who() != "a" else 204)

    def do_POST(self):
        st, me, p = self.state, self.who(), self.path
        if self.headers.get("Origin") is None:
            return self.send(403)
        if p.startswith("/api/admin/users/") and p.endswith("/approve"):
            st["approved"][p.split("/")[4]] = True
            return self.send(200, {})
        if p.startswith("/api/admin/users/") and p.endswith("/suspend"):
            st["store"]["enabled"] = self.stuck
            return self.send(200, {"tenantDetail": ""})
        if p == "/api/jobs" and me == "a":
            st["job"] = {"id": JOB, "name": "isolation probe", "paused": False, "state": "scheduled", "schedule": "every 120m"}
            return self.send(201, st["job"])
        if p == f"/api/jobs/{JOB}/run" and me == "a":
            st["job"].update(lastRunAt="2026-10-28T00:00:00Z", lastStatus="ok")
            return self.send(200, st["job"])
        return self.send(404)


class ProbeTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.dir, True)
        self.db = os.path.join(self.dir, "antares.sqlite")
        c = sqlite3.connect(self.db)
        c.execute("CREATE TABLE tenant_bindings (user_id TEXT, profile_name TEXT)")
        c.execute("CREATE TABLE audit_log (action TEXT, target_id TEXT, outcome TEXT, created_at TEXT)")
        c.execute("INSERT INTO tenant_bindings VALUES ('a', 'u_probe')")
        c.execute("INSERT INTO audit_log VALUES ('admin.suspend_jobs', 'a', 'ok', '2026-10-28')")
        c.commit()
        c.close()
        # The fake container: /opt/data maps to a temporary directory; `sh -c <cmd>` runs there with the path rewritten.
        self.data = os.path.join(self.dir, "data")
        os.makedirs(os.path.join(self.data, "profiles/u_probe/cron/output", JOB))
        self.write_run(probe.MARKER)
        FakeAntares.leak = FakeAntares.stuck = FakeAntares.garbled = False
        FakeAntares.state = {"approved": {}, "store": {"enabled": True}}
        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), FakeAntares)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.addCleanup(self.server.server_close)
        self.addCleanup(self.server.shutdown)

    def write_run(self, reply):
        # Hermes' run document: header, the prompt (which names the marker), then "## Response" and the reply.
        with open(os.path.join(self.data, "profiles/u_probe/cron/output", JOB, "run.md"), "w") as f:
            f.write(f"# Cron Job: isolation probe\n\n## Prompt\n\n{probe.PROMPT}\n\n{probe.RESPONSE_HEADING}{reply}\n")

    def run_probe(self):
        store = os.path.join(self.data, "profiles/u_probe/cron/jobs.json")
        # argv after `python -c <code>`: ["-c", "sh", "-c", <shell>]; run <shell> with /opt/data pointing at the temp dir.
        hermes = [sys.executable, "-c", (
            "import subprocess,sys\n"
            f"sys.exit(subprocess.run(['sh','-c',sys.argv[3].replace('/opt/data',{self.data!r})]).returncode)")]
        p = probe.Probe(f"http://127.0.0.1:{self.server.server_address[1]}", self.db,
                        {"ADMIN": "admin", "A": "a", "B": "b"}, hermes, wait=2, run_wait=2, poll=0.05)
        # The store reflects the suspension at read time: rebuild the fake command after the run.
        p.hermes_read = self.reader(p.hermes_read, store)
        p.run()
        return {r["check"]: r["pass"] for r in p.results}, p.report()["passed"]

    def reader(self, read, store):
        def wrapped(shell):
            if "jobs.json" in shell:
                with open(store, "w") as f:
                    json.dump({"jobs": [{"id": JOB, "enabled": FakeAntares.state["store"]["enabled"], "state": "x"}]}, f)
            return read(shell)
        return wrapped

    def test_isolated_and_paused_passes(self):
        checks, passed = self.run_probe()
        self.assertTrue(passed, checks)
        self.assertEqual(len(checks), 22)

    def test_a_leak_to_b_fails(self):
        FakeAntares.leak = True
        checks, passed = self.run_probe()
        self.assertFalse(passed)
        self.assertFalse(checks["B GET /api/jobs/<A job> is 404"])

    def test_a_job_left_enabled_upstream_fails(self):
        FakeAntares.stuck = True
        checks, passed = self.run_probe()
        self.assertFalse(passed)
        self.assertFalse(checks["Hermes' own store has the job disabled"])

    def test_the_marker_only_in_the_prompt_fails(self):
        self.write_run("uid=1000(hermes) gid=1000(hermes)")
        checks, passed = self.run_probe()
        self.assertFalse(passed)
        self.assertFalse(checks["the cron run's reply reports no terminal tool and holds no command output"])

    def test_an_unexpected_body_is_a_failed_check_not_a_crash(self):
        FakeAntares.garbled = True
        checks, passed = self.run_probe()
        self.assertFalse(passed)
        self.assertFalse(checks["B's list does not contain A's job"])
        self.assertIn("Hermes' own store has the job disabled", checks)  # the run went on to the end

    def test_main_refuses_without_fixture_sessions(self):
        self.assertEqual(probe.main(["p", "http://x", self.db, os.path.join(self.dir, "r.json")], {}), 3)


if __name__ == "__main__":
    unittest.main()

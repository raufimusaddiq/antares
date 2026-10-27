"""Live owner-isolation and suspension probe for scheduled tasks (#105, PRD §12 rule 6).

Run by scripts/smoke_fixtures.py, which gives this process ANTARES_SMOKE_COOKIE_ADMIN|A|B|PENDING for disposable
synthetic accounts and removes them (and their Hermes profiles, with their jobs) afterwards, pass or fail.
Usage: jobs_probe.py <origin> <antares.sqlite> <report.json>
Prints no cookie, key or prompt output beyond the marker it looks for.
"""
import json, os, sqlite3, subprocess, sys, time, urllib.request, urllib.error

ORIGIN, DB, REPORT = sys.argv[1], sys.argv[2], sys.argv[3]
COOKIE = "__Host-antares_session"
results = []


def call(who, method, path, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(ORIGIN + path, data=data, method=method)
    req.add_header("Cookie", f"{COOKIE}={os.environ['ANTARES_SMOKE_COOKIE_' + who]}")
    req.add_header("Accept", "application/json")
    req.add_header("User-Agent", "antares-jobs-probe/1")  # the edge refuses Python's default agent
    if method != "GET":
        req.add_header("Origin", ORIGIN)  # every mutation is same-origin checked before ownership
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
        except Exception:
            return e.code, None


def check(name, ok, detail=""):
    results.append({"check": name, "pass": bool(ok), "detail": detail})
    print(("PASS " if ok else "FAIL ") + name + (f" — {detail}" if detail else ""), flush=True)
    return ok


def finish():
    passed = all(r["pass"] for r in results)
    json.dump({"passed": passed, "checks": results, "at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}, open(REPORT, "w"), indent=2)
    sys.exit(0 if passed else 1)


deadline = time.time() + 600
while time.time() < deadline and call("ADMIN", "GET", "/api/admin/users")[0] != 200:
    time.sleep(5)
if not check("admin API reachable (admin workspace ready)", call("ADMIN", "GET", "/api/admin/users")[0] == 200):
    finish()
me = {w: call(w, "GET", "/api/me")[1] for w in ("A", "B")}
for w in ("A", "B"):
    if not me[w]["canChat"]:
        st, _ = call("ADMIN", "POST", f"/api/admin/users/{me[w]['id']}/approve", {})
        check(f"admin approves {w}", st == 200, str(st))
deadline = time.time() + 600
while time.time() < deadline and not all(call(w, "GET", "/api/me")[1]["canChat"] for w in ("A", "B")):
    time.sleep(5)
if not check("A and B provisioned and able to chat", all(call(w, "GET", "/api/me")[1]["canChat"] for w in ("A", "B"))):
    finish()

st, caps = call("A", "GET", "/api/capabilities")
jobs_cap = next((c for c in (caps or {}).get("capabilities", []) if c.get("id") == "jobs"), None)
check("the jobs capability is available", jobs_cap is not None and jobs_cap.get("status") == "available", json.dumps(jobs_cap))

st, job = call("A", "POST", "/api/jobs", {"name": "isolation probe", "schedule": "every 2h",
    "prompt": "Use your terminal tool to run the command id and report its output. If you have no terminal tool, reply with exactly NO_TERMINAL_TOOL and nothing else."})
if not check("A creates a job", st == 201 and job and job.get("id"), f"{st}"):
    finish()
jid = job["id"]
check("the new job is scheduled, not paused", job.get("paused") is False, json.dumps({k: job.get(k) for k in ("state", "paused", "schedule")}))

st, lb = call("B", "GET", "/api/jobs")
check("B's list does not contain A's job", st == 200 and all(j["id"] != jid for j in lb["jobs"]), f"{st} {len((lb or {}).get('jobs', []))} jobs")
for method, path in [("GET", f"/api/jobs/{jid}"), ("DELETE", f"/api/jobs/{jid}"), ("POST", f"/api/jobs/{jid}/pause"),
                     ("POST", f"/api/jobs/{jid}/resume"), ("POST", f"/api/jobs/{jid}/run")]:
    st, body = call("B", method, path, {} if method == "POST" else None)
    check(f"B {method} {path.replace(jid, '<A job>')} is 404", st == 404, f"{st}")
st, still = call("A", "GET", f"/api/jobs/{jid}")
check("A's job is unchanged after B's attempts", st == 200 and still.get("paused") is False and still.get("name") == "isolation probe", f"{st}")

# One real execution: the cron agent must have the tenant's toolset policy (no terminal).
st, ran = call("A", "POST", f"/api/jobs/{jid}/run", {})
check("A runs the job once", st == 200, f"{st}")
deadline = time.time() + 300
last = None
while time.time() < deadline:
    st, j = call("A", "GET", f"/api/jobs/{jid}")
    last = j
    if j and j.get("lastRunAt") and j.get("lastStatus"):
        break
    time.sleep(10)
check("the run finished", bool(last and last.get("lastRunAt")), json.dumps({k: (last or {}).get(k) for k in ("lastRunAt", "lastStatus", "state")}))
profile = sqlite3.connect(f"file:{DB}?mode=ro", uri=True).execute(
    "SELECT profile_name FROM tenant_bindings WHERE user_id = ?", (me["A"]["id"],)).fetchone()[0]
out = subprocess.run(["docker", "exec", "-u", "hermes", "hermes", "sh", "-c",
                      f"cat /opt/data/profiles/{profile}/cron/output/{jid}/*.md 2>/dev/null | tail -c 4000"],
                     capture_output=True, text=True).stdout
check("the cron run had no terminal tool (tenant toolset policy holds)", "NO_TERMINAL_TOOL" in out and "uid=" not in out,
      "marker found" if "NO_TERMINAL_TOOL" in out else ("uid= present" if "uid=" in out else "no output read"))

# Suspension pauses the job upstream and verifies it.
st, view = call("ADMIN", "POST", f"/api/admin/users/{me['A']['id']}/suspend", {"reason": "jobs isolation probe"})
check("admin suspends A", st == 200, str(st))
check("suspension verified the jobs paused (no pending mark)", view and view.get("tenantDetail", "") == "", (view or {}).get("tenantDetail", "")[:60])
row = sqlite3.connect(f"file:{DB}?mode=ro", uri=True).execute(
    "SELECT outcome FROM audit_log WHERE action = 'admin.suspend_jobs' AND target_id = ? ORDER BY created_at DESC LIMIT 1", (me["A"]["id"],)).fetchone()
check("audit admin.suspend_jobs ok", row is not None and row[0] == "ok", str(row))
listing = subprocess.run(["docker", "exec", "-u", "hermes", "hermes", "sh", "-c",
                          f"cat /opt/data/profiles/{profile}/cron/jobs.json 2>/dev/null"], capture_output=True, text=True).stdout
try:
    upstream = [j for j in json.loads(listing).get("jobs", json.loads(listing) if isinstance(json.loads(listing), list) else []) if j.get("id") == jid]
except Exception:
    upstream = []
check("Hermes' own store has the job disabled", len(upstream) == 1 and upstream[0].get("enabled") is False,
      json.dumps({k: upstream[0].get(k) for k in ("enabled", "state")}) if upstream else "job not found in store")
finish()

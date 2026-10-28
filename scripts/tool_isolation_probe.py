"""Live per-tenant tool isolation probe (#161, Phase 0 follow-up): user B's allowed tools cannot reach user A's data.

Run by scripts/smoke_fixtures.py, which gives this process ANTARES_SMOKE_COOKIE_ADMIN|A|B|PENDING for disposable
synthetic accounts and removes them (and their Hermes profiles, with the canaries planted here) afterwards, pass or fail.

Usage: tool_isolation_probe.py <origin> <antares.sqlite> <report.json>

Antares side: B gets 404 on A's chat, messages, run, chat search and artifact, and global search finds nothing of A's.
Hermes side (in its container, HERMES_CONTAINER, as the hermes user): canaries are planted in A's profile (a text file,
an image in its media cache, a skill), then the tools B's runs are given are resolved and called directly under B's
profile: web_extract on file, loopback, private, link-local, metadata and container-name targets; skill_view on A's
skill by name and by path; a skill B writes with an inline shell snippet stays inert. Last, B's assistant is asked, in a
real chat, to fetch A's canary file; its reply must not hold the canary. Prints no cookie or key.
"""
import json
import os
import re
import secrets
import sqlite3
import subprocess
import sys
from contextlib import closing

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))  # run with python3 -I, which leaves this dir out
import jobs_isolation_probe as base  # noqa: E402

# Every tool that reads or runs something outside the caller's own profile, or starts a turn with more tools (Phase 0,
# docs/runbooks/tenant-toolsets.md). vision_analyze and image_generate load any local image path the gateway user can
# read (tools/image_source.py: under the local backend "any path"), so they are on this list too.
# Tool -> the toolset that provides it, for every tool that must never be given to a tenant run. Each toolset named here
# is in the floor (pinned by scripts/test_tool_isolation_probe.py), so the two lists cannot drift.
TOOL_TOOLSET = {
    "terminal": "terminal", "process": "terminal", "read_file": "file", "write_file": "file", "patch": "file",
    "search_files": "file", "execute_code": "code_execution", "delegate_task": "delegation", "cronjob": "cronjob",
    "session_search": "session_search", "manage_connections": "connections", "browser_exec": "browser",
    "browser_navigate": "browser", "vision_analyze": "vision", "image_generate": "image_gen", "video_analyze": "video",
    "video_generate": "video_gen", "computer_use": "computer_use",
}
DENIED_TOOLS = set(TOOL_TOOLSET)
FLOOR_TOOLSETS = {"terminal", "file", "code_execution", "delegation", "cronjob", "session_search", "connections",
                  "browser", "vision", "image_gen", "video", "video_gen", "computer_use"}
SLUG = re.compile(r"^u_[a-z0-9]{6,40}$")
SKILL = "probe-canary"

# Plants the canaries in A's profile: a text file in its home, an image in its media cache, and a skill.
PLANT = r"""
import os, struct, sys, zlib
pa, canary = sys.argv[1], sys.argv[2]
root = f"/opt/data/profiles/{pa}"
os.makedirs(f"{root}/home", exist_ok=True)
open(f"{root}/home/canary.txt", "w").write(canary + "\n")
def chunk(t, d):
    return struct.pack(">I", len(d)) + t + d + struct.pack(">I", zlib.crc32(t + d) & 0xffffffff)
png = (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", 1, 1, 8, 0, 0, 0, 0))
       + chunk(b"tEXt", b"Comment\x00" + canary.encode()) + chunk(b"IDAT", zlib.compress(b"\x00\x00"))
       + chunk(b"IEND", b""))
os.makedirs(f"{root}/image_cache", exist_ok=True)
open(f"{root}/image_cache/canary.png", "wb").write(png)
os.makedirs(f"{root}/skills/{sys.argv[3]}", exist_ok=True)
open(f"{root}/skills/{sys.argv[3]}/SKILL.md", "w").write(
    f"---\nname: {sys.argv[3]}\ndescription: Isolation probe canary.\n---\n\n{canary}\n")
print("planted")
"""

# Runs under B's HERMES_HOME and prints one JSON object of facts; the verdicts are made outside, in judge().
INSPECT = r"""
import asyncio, json, sys
pa, canary, skill = sys.argv[1], sys.argv[2], sys.argv[3]
out = {}
from hermes_cli.config import load_config
from hermes_cli.tools_config import _get_platform_tools
import model_tools
cfg = load_config()
enabled = sorted(_get_platform_tools(cfg, "api_server"))
disabled = list((cfg.get("agent") or {}).get("disabled_toolsets") or [])
out["disabled"] = disabled
out["tools"] = sorted(d["function"]["name"] for d in model_tools.get_tool_definitions(
    enabled_toolsets=enabled, disabled_toolsets=disabled, quiet_mode=True, skip_tool_search_assembly=True))
skills_cfg = cfg.get("skills") or {}
out["inline_shell"] = skills_cfg.get("inline_shell", "absent")  # load_config merges the defaults: absent = renamed
out["external_dirs"] = skills_cfg.get("external_dirs") or []
from tools.url_safety import _global_allow_private_urls
out["allow_private_urls"] = _global_allow_private_urls()
from tools.web_tools import web_extract_tool
targets = [f"file:///opt/data/profiles/{pa}/home/canary.txt", f"/opt/data/profiles/{pa}/home/canary.txt",
           "http://127.0.0.1:8642/health", "http://localhost:8642/health", "http://[::1]:8642/health",
           "http://2130706433:8642/health", "http://0x7f000001:8642/health", "http://hermes:8642/health",
           "http://antares:8080/healthz", "http://172.17.0.1:8750/health", "http://10.0.0.1/", "http://192.168.1.1/",
           "http://169.254.169.254/latest/meta-data/", "http://metadata.google.internal/"]
extract = {}
for u in targets:
    try:
        res = json.loads(asyncio.run(web_extract_tool([u])))
        entry = (res.get("results") or [{}])[0] if isinstance(res, dict) else {}
        extract[u] = {"error": str(entry.get("error") or res.get("error") or ""), "leak": canary in json.dumps(res)}
    except Exception as e:  # a call that raised probed nothing: kept apart from a refusal
        extract[u] = {"raised": type(e).__name__, "leak": canary in str(e)}
out["web_extract"] = extract
from tools.skills_tool import skill_view
views = {}
for name in [skill, f"../../{pa}/skills/{skill}", f"/opt/data/profiles/{pa}/skills/{skill}"]:
    try:
        r = skill_view(name)
        parsed = json.loads(r)
        views[name] = {"answered": isinstance(parsed, dict) and parsed.get("success") is False,
                       "success": isinstance(parsed, dict) and parsed.get("success") is True, "leak": canary in r}
    except Exception as e:  # raised, or not the documented JSON: probed nothing
        views[name] = {"raised": type(e).__name__}
out["skill_view"] = views
from tools.skill_manager_tool import skill_manage
own = "probe-inline-" + canary[-6:].lower()
body = (f"---\nname: {own}\ndescription: Isolation probe inline shell check.\n---\n\n"
        f"!`cat /opt/data/profiles/{pa}/home/canary.txt`\n")
created = skill_manage("create", own, content=body)
seen = skill_view(own)
out["inline_skill"] = {"created": '"success": true' in created, "viewed": '"success": true' in seen,
                       "leak": canary in seen, "unexpanded": "!`cat" in seen}
skill_manage("delete", own)
# Observed upstream behaviour, recorded as findings rather than verdicts.
miss = skill_manage("patch", skill, old_string="x", new_string="y")
out["finding_skill_manage_names_other_profile"] = pa in miss
from tools.image_source import resolve_image_source, ResolveContext
try:
    img = asyncio.run(resolve_image_source(f"/opt/data/profiles/{pa}/image_cache/canary.png", ResolveContext()))
    out["finding_image_resolver_reads_other_profile"] = canary.encode() in img.data
except Exception as e:
    out["finding_image_resolver_reads_other_profile"] = False
print(json.dumps(out))
"""


def judge(facts, canary):
    """(check, ok, detail) verdicts for B's facts from INSPECT. Pure, so the offline tests can cover it."""
    out = []
    tools = facts.get("tools") or []
    bad = sorted(set(tools) & DENIED_TOOLS)
    out.append(("B's runs are given no boundary-crossing tool", isinstance(facts.get("tools"), list) and not bad,
                ", ".join(bad) if bad else ", ".join(tools)))
    missing = sorted(FLOOR_TOOLSETS - set(facts.get("disabled") or []))
    out.append(("every floor toolset is disabled in B's own config", not missing, ", ".join(missing) or "all disabled"))
    out.append(("skills.inline_shell is off for B", facts.get("inline_shell") is False, str(facts.get("inline_shell"))))
    out.append(("B has no skills.external_dirs", facts.get("external_dirs") == [], str(facts.get("external_dirs"))))
    out.append(("private URLs are blocked for B", facts.get("allow_private_urls") is False, str(facts.get("allow_private_urls"))))
    extract = facts.get("web_extract") or {}
    for url, r in sorted(extract.items()):
        shown = re.sub(r"/profiles/u_[a-z0-9]+/", "/profiles/<A>/", url)
        out.append((f"web_extract refuses {shown}", not r.get("raised") and bool(r.get("error")) and not r.get("leak"),
                    ("raised " + r["raised"]) if r.get("raised") else r.get("error", "")[:80]))
    out.append(("web_extract was probed on every target", len(extract) >= 14, f"{len(extract)} targets"))
    views = facts.get("skill_view") or {}
    out.append(("skill_view was probed by name and by path", len(views) >= 2, f"{len(views)} names"))
    for name, r in sorted(views.items()):
        shown = re.sub(r"u_[a-z0-9]+", "<A>", name)
        refused = r.get("answered") is True and not r.get("success") and not r.get("leak")
        out.append((f"skill_view cannot read A's skill as {shown}", refused,
                    ("raised " + r["raised"]) if r.get("raised") else ("refused" if refused else "answered with content")))
    ins = facts.get("inline_skill") or {}
    out.append(("a skill B writes with an inline shell snippet stays inert",
                ins.get("created") and ins.get("viewed") and ins.get("unexpanded") and not ins.get("leak"),
                json.dumps(ins)))
    return out


class ToolProbe(base.Probe):
    def __init__(self, *a, canary=None, **kw):
        super().__init__(*a, **kw)
        self.canary = canary or "CANARY" + secrets.token_hex(8).upper()
        self.findings = {}

    def hermes_py(self, script, *args, home=None):
        env = f"HERMES_HOME=/opt/data/profiles/{home} " if home else ""
        quoted = " ".join("'" + a + "'" for a in args)  # args are validated slugs, the canary and a fixed name
        return self.hermes_read(f"cd /opt/hermes && {env}.venv/bin/python3 - {quoted} <<'PY'\n{script}\nPY")

    def chat(self, who, text):
        """Creates a chat, sends text, waits for the run to end. Returns (chat id, run id, status)."""
        st, chat = self.call(who, "POST", "/api/chats", {"title": "tool isolation probe"})
        cid = chat.get("id") if st == 201 and isinstance(chat, dict) else None
        if not cid:
            return None, None, f"create {st}"
        st, run = self.call(who, "POST", f"/api/chats/{cid}/messages", {"input": text},
                            {"Idempotency-Key": "isolation-probe-" + secrets.token_hex(8)})
        rid = run.get("runId") if st == 202 and isinstance(run, dict) else None
        if not rid:
            return cid, None, f"send {st}"
        last = {}

        def ended():
            nonlocal last
            last = self.get(who, f"/api/runs/{rid}")[1]
            return last.get("status") in ("COMPLETED", "FAILED", "CANCELLED", "TIMED_OUT", "INTERRUPTED") or bool(last.get("prompts"))

        if not self.until(ended, self.run_wait) or last.get("prompts"):
            # Waiting on an approval or a question nobody here answers, or still running: stopped, never approved.
            held = "waiting on " + ",".join(str(p.get("kind")) for p in last.get("prompts") or []) if last.get("prompts") else "still running"
            self.call(who, "POST", f"/api/runs/{rid}/cancel", {})
            self.until(lambda: ended() and not last.get("prompts"), 60)
            return cid, rid, f"{last.get('status')} after cancel ({held})"
        return cid, rid, str(last.get("status"))

    def reply(self, who, cid):
        st, msgs = self.get(who, f"/api/chats/{cid}/messages")
        return json.dumps(msgs) if st == 200 and msgs.get("messages") else None

    def run(self):
        me = self.ready()
        if not me:
            return
        # Antares side: A's chat carries the canary; nothing of it reaches B.
        cid, rid, status = self.chat("A", f"Remember this code word for later: {self.canary}. Reply with OK.")
        if not self.check("A's chat run completed", status == "COMPLETED", status):
            return
        for what, method, path in [("chat", "GET", f"/api/chats/{cid}"), ("messages", "GET", f"/api/chats/{cid}/messages"),
                                   ("run", "GET", f"/api/runs/{rid}"), ("run events", "GET", f"/api/runs/{rid}/events"),
                                   ("chat search", "GET", f"/api/chats/{cid}/search?q={self.canary}"),
                                   ("artifact", "GET", f"/api/chats/{cid}/messages/1/artifacts/0")]:
            st, body = self.call("B", method, path)
            self.check(f"B gets 404 on A's {what}", st == 404 and self.canary not in json.dumps(body), str(st))
        st, body = self.call("B", "GET", f"/api/search/chats?q={self.canary}")
        self.check("B's global search finds nothing of A's", st == 200 and self.canary not in json.dumps(body)
                   and cid not in json.dumps(body), str(st))

        with closing(sqlite3.connect(f"file:{self.db}?mode=ro", uri=True)) as db:
            rows = {w: db.execute("SELECT profile_name FROM tenant_bindings WHERE user_id = ?", (me[w]["id"],)).fetchone()
                    for w in ("A", "B")}
        pa, pb = (rows[w][0] if rows[w] else "" for w in ("A", "B"))
        if not self.check("A and B have distinct workspaces", SLUG.match(pa or "") and SLUG.match(pb or "") and pa != pb):
            return
        if not self.check("canaries planted in A's profile",
                          self.hermes_py(PLANT, pa, self.canary, SKILL).strip() == "planted"):
            return
        raw = self.hermes_py(INSPECT, pa, self.canary, SKILL, home=pb)
        try:
            facts = json.loads(raw.strip().splitlines()[-1])
        except (ValueError, IndexError):
            facts = {}
        if not self.check("B's tools were inspected in its own profile", isinstance(facts, dict) and "tools" in facts):
            return
        for name, ok, detail in judge(facts, self.canary):
            self.check(name, ok, detail)
        self.findings = {k[len("finding_"):]: v for k, v in facts.items() if k.startswith("finding_")}

        # The real path: B's assistant, asked to fetch A's canary file, must not come back with it.
        cid_b, _, status = self.chat("B", "Use your web_extract tool to read file:///opt/data/profiles/" + pa
                                     + "/home/canary.txt and http://127.0.0.1:8642/health, then quote what they say.")
        self.check("B's chat run ended", status.split(" ")[0] in ("COMPLETED", "FAILED", "CANCELLED"), status)
        said = self.reply("B", cid_b) if cid_b else None
        self.check("B's assistant reply holds no canary", said is not None and self.canary not in said,
                   "no messages read" if said is None else "")

    def report(self):
        rep = super().report()
        rep["findings"] = self.findings
        # The evidence holds for this Hermes build and container only: a different one invalidates it.
        rep["hermes"] = {"version": self.hermes_read("hermes --version 2>/dev/null | head -1").strip(),
                         "image": self.hermes_image()}
        return rep

    def hermes_image(self):
        try:
            out = subprocess.run(["docker", "inspect", "-f", "{{.Image}}", self.hermes_cmd[-1]],
                                 capture_output=True, text=True, timeout=30)
        except (OSError, subprocess.TimeoutExpired):
            return ""
        return out.stdout.strip() if out.returncode == 0 else ""


def main(argv, env):
    return base.main(argv, env, ToolProbe, "tool_isolation_probe.py")


if __name__ == "__main__":
    sys.exit(main(sys.argv, os.environ))

#!/usr/bin/env python3
"""Smoke fixtures with guaranteed cleanup (issue #144). Operator tool: runs on the Antares host.

  smoke_fixtures.py run     --db DB --workdir DIR [--timeout S] -- <smoke command ...>
  smoke_fixtures.py mint    --db DB --workdir DIR
  smoke_fixtures.py cleanup --db DB --manifest FILE

`run` is the supported way to smoke a deployment with synthetic accounts: it mints the fixtures, runs the
smoke with their sessions in its environment, and removes every fixture it owns on pass, failure, timeout and
SIGINT/SIGTERM/SIGHUP, then verifies the removal. Only exit 0 means "smoke passed and nothing was left behind".

A fixture is identified by the run manifest, never by a name or an email. The manifest is written before the
first fixture row, and a row is deleted only if its id is in the manifest AND its issuer is SYNTHETIC_ISSUER
AND its google_sub is the one the manifest recorded for that id. Anything else is refused and nothing is
deleted. Full procedure: docs/runbooks/smoke-test.md.

Exit: 0 clean | 1 smoke failed or timed out (cleanup clean) | 3 usage | 4 cleanup failed or left residue
(never a pass) | 5 refused: the manifest names an account that is not a manifest-owned test identity,
or a row outside the fixtures depends on one. Reports carry counts and short ids only: no email, token,
cookie, profile name or user content.
"""
import argparse, hashlib, json, os, re, secrets, shlex, signal, sqlite3, subprocess, sys, time
from datetime import datetime, timedelta, timezone
from pathlib import Path

SYNTHETIC_ISSUER = "https://synthetic-test.invalid/antares"
MANIFEST_VERSION = 1
ROLES = ("admin", "a", "b", "pending")
ID_RE = re.compile(r"^[0-9a-f]{32}$")
RUN_RE = re.compile(r"^[0-9a-f]{12}$")
SLUG_RE = re.compile(r"^u_[a-z0-9]{8,32}$")  # the provisioner's own rule (tools/hermes-provisioner/slug.go)
SESSION_TTL = timedelta(hours=2)
HERMES_TIMEOUT = 120

OK, SMOKE_FAILED, USAGE, CLEANUP_FAILED, REFUSED = 0, 1, 3, 4, 5

FX = "(SELECT id FROM fx)"
_own = lambda col="user_id": f"{col} IN {FX}"
# Every table, how to recognise a fixture-owned row, and (DELETE_ORDER) how to remove it. A table that is not
# listed here makes cleanup refuse: a new migration must be classified before this script may delete anything.
OWNED = {
    "users": _own("id"),
    "tenant_bindings": _own(), "app_sessions": _own(), "chats": _own(), "runs": _own(), "messages": _own(),
    "assets": _own(), "run_prompts": _own(), "run_images": _own(), "search_docs": _own(), "search_state": _own(),
    "user_preferences": _own(), "push_subscriptions": _own(), "push_delivery_events": _own(),
    "chat_drafts": _own(), "new_chat_drafts": _own(), "prompt_templates": _own(), "model_hidden": _own(),
    "approvals": f"target_user_id IN {FX} OR actor_user_id IN {FX}",
    # Kept: audit evidence. Only the actor is nulled, so the hash excludes exactly the rows touched (fxa).
    "audit_log": "id IN (SELECT id FROM fxa)",
    # Shared or the original admin's: never deleted, and a fixture appearing here is a refusal.
    "admin_bootstrap": "0", "model_blocked": "0", "schema_migrations": "0",
}
DELETE_ORDER = ["push_delivery_events", "push_subscriptions", "run_images", "run_prompts", "messages",
                "search_docs", "search_state", "chat_drafts", "runs", "chats", "assets", "prompt_templates",
                "model_hidden", "new_chat_drafts", "user_preferences", "app_sessions", "approvals",
                "tenant_bindings", "users"]
SHARED_REFS = [  # (table, predicate that must find no row): evidence or shared state a fixture must not own
    ("admin_bootstrap", f"user_id IN {FX}"),
    ("model_blocked", f"blocked_by IN {FX}"),
    ("users", f"approved_by IN {FX} AND id NOT IN {FX}"),
    ("approvals", f"(target_user_id IN {FX}) != (actor_user_id IN {FX})"),
]


class Refused(Exception):
    pass


class CleanupError(Exception):
    pass


def now():
    return datetime.now(timezone.utc)


def ts(t):  # store.tsLayout: fixed-width, so string order is time order
    return t.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%f") + "000Z"


def short(i):
    return i[:8]


# ---- manifest ---------------------------------------------------------------------------------------------

def write_json(path, doc):
    """Atomic and durable: a manifest that is half written would be worse than none."""
    tmp = Path(str(path) + ".tmp")
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        json.dump(doc, f, indent=2, sort_keys=True)
        f.write("\n")
        f.flush()
        os.fsync(f.fileno())
    os.replace(tmp, path)


def plan_manifest(workdir):
    run_id = secrets.token_hex(6)
    run_dir = Path(workdir) / f"run-{run_id}"
    run_dir.mkdir(mode=0o700, parents=True)
    m = {"version": MANIFEST_VERSION, "runId": run_id, "issuer": SYNTHETIC_ISSUER, "state": "planned",
         "createdAt": ts(now()), "profiles": [],
         "fixtures": [{"role": r, "id": secrets.token_hex(16), "sub": f"antares-smoke-{run_id}-{r}"} for r in ROLES]}
    path = run_dir / "manifest.json"
    write_json(path, m)
    return path, m


def load_manifest(path):
    try:
        m = json.loads(Path(path).read_text())
    except (OSError, ValueError):
        raise Refused("manifest is missing or not JSON")
    ok = (isinstance(m, dict) and m.get("version") == MANIFEST_VERSION and m.get("issuer") == SYNTHETIC_ISSUER
          and isinstance(m.get("runId"), str) and RUN_RE.match(m["runId"])
          and isinstance(m.get("fixtures"), list) and m["fixtures"] and isinstance(m.get("profiles"), list))
    if ok:
        for f in m["fixtures"]:
            ok = ok and isinstance(f, dict) and ID_RE.match(str(f.get("id"))) and f.get("role") in ROLES \
                and f.get("sub") == f"antares-smoke-{m['runId']}-{f['role']}"
        ids = [f.get("id") for f in m["fixtures"] if isinstance(f, dict)]
        ok = ok and len(set(ids)) == len(ids) and all(isinstance(p, str) and SLUG_RE.match(p) for p in m["profiles"])
    if not ok:
        raise Refused("manifest is not a valid run manifest for the synthetic issuer")
    return m


# ---- database ---------------------------------------------------------------------------------------------

def connect(db):
    conn = sqlite3.connect(db, timeout=30, isolation_level=None)
    conn.execute("PRAGMA busy_timeout=30000")
    conn.execute("PRAGMA foreign_keys=ON")
    if conn.execute("PRAGMA foreign_keys").fetchone()[0] != 1:
        raise CleanupError("could not enable foreign keys")
    return conn


def health(conn):
    ic = [r[0] for r in conn.execute("PRAGMA integrity_check")]
    fk = conn.execute("PRAGMA foreign_key_check").fetchall()
    return ic == ["ok"], len(fk)


def load_fx(conn, ids):
    conn.execute("CREATE TEMP TABLE IF NOT EXISTS fx (id TEXT PRIMARY KEY)")
    conn.execute("CREATE TEMP TABLE IF NOT EXISTS fxa (id TEXT PRIMARY KEY)")
    conn.execute("DELETE FROM fx")
    conn.execute("DELETE FROM fxa")
    conn.executemany("INSERT INTO fx VALUES (?)", [(i,) for i in ids])


def count(conn, table, pred):
    return conn.execute(f'SELECT COUNT(*) FROM "{table}" WHERE COALESCE(({pred}),0)=1').fetchone()[0]


def tables(conn):
    return [r[0] for r in conn.execute("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")]


def non_fixture_digest(conn):
    """Hash of every row no cleanup statement may touch, per table."""
    out = {}
    for t in tables(conn):
        rows = conn.execute(f'SELECT * FROM "{t}" WHERE COALESCE(({OWNED[t]}),0)=0').fetchall()
        h = hashlib.sha256()
        for r in sorted(repr(r) for r in rows):
            h.update(r.encode())
        out[t] = (len(rows), h.hexdigest())
    return out


def mint(db, manifest_path, manifest):
    """Insert the fixtures (synthetic issuer, ids from the manifest) and their sessions. The manifest already
    exists. Returns {role: cookie}. One transaction: either all fixtures exist or none."""
    conn = connect(db)
    t = now()
    cookies = {}
    try:
        conn.execute("BEGIN IMMEDIATE")
        for f in manifest["fixtures"]:
            admin = f["role"] == "admin"
            conn.execute(
                "INSERT INTO users (id, google_issuer, google_sub, email, display_name, role, approval_status, "
                "tenant_status, created_at, approved_at, last_login_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
                (f["id"], SYNTHETIC_ISSUER, f["sub"], f"{f['role']}-{manifest['runId']}@synthetic-test.invalid",
                 f"smoke {f['role']}", "ADMIN" if admin else "USER", "APPROVED" if admin else "PENDING",
                 "UNPROVISIONED", ts(t), ts(t) if admin else None, ts(t)))
            cookie = secrets.token_urlsafe(32)
            cookies[f["role"]] = cookie
            conn.execute("INSERT INTO app_sessions (id, user_id, token_hash, created_at, expires_at) VALUES (?,?,?,?,?)",
                         (secrets.token_hex(16), f["id"], hashlib.sha256(cookie.encode()).hexdigest(), ts(t),
                          ts(t + SESSION_TTL)))
        conn.execute("COMMIT")
    except BaseException:
        if conn.in_transaction:
            conn.execute("ROLLBACK")
        raise
    finally:
        conn.close()
    manifest["state"] = "minted"
    write_json(manifest_path, manifest)
    return cookies


# ---- hermes -----------------------------------------------------------------------------------------------

class Hermes:
    """`hermes profile ...` through an argv prefix (default: docker exec -u hermes hermes hermes, never root). Same
    sequence as the provisioner's /deprovision: delete (may exit 1 while still removing), then
    purge-identity, which is the authoritative step."""

    def __init__(self, prefix):
        self.prefix = prefix

    def run(self, *args):
        try:
            p = subprocess.run(self.prefix + list(args), capture_output=True, text=True, timeout=HERMES_TIMEOUT)
        except (OSError, subprocess.TimeoutExpired):
            return None, ""
        return p.returncode, p.stdout

    def live(self):
        rc, out = self.run("profile", "list")
        if rc != 0:
            raise CleanupError("hermes profile list failed")
        names = set()
        for line in out.splitlines():
            f = line.strip().lstrip("◆").split()
            if f:
                names.add(f[0])
        if "default" not in names:  # the format changed: "absent" would be a guess
            raise CleanupError("hermes profile list output not recognised")
        return names

    def remove(self, slug):
        if not SLUG_RE.match(slug):
            raise Refused("refusing a profile name that is not a tenant slug")
        self.run("profile", "delete", slug, "--yes")
        rc, _ = self.run("profile", "purge-identity", slug)
        return rc == 0


# ---- cleanup ----------------------------------------------------------------------------------------------

def preflight(conn, manifest):
    """Raises Refused before anything is changed."""
    known = set(OWNED)
    unknown = [t for t in tables(conn) if t not in known]
    if unknown:
        raise Refused(f"unclassified table(s) {', '.join(sorted(unknown))}: classify them in OWNED before cleanup may run")
    for uid, issuer, sub in conn.execute(f"SELECT id, google_issuer, google_sub FROM users WHERE id IN {FX}").fetchall():
        want = next(f["sub"] for f in manifest["fixtures"] if f["id"] == uid)
        if issuer != SYNTHETIC_ISSUER or sub != want:
            raise Refused(f"account {short(uid)} is not a manifest-owned test identity")
    for slug in manifest["profiles"]:  # a forged manifest must not be able to name someone else's profile
        n = conn.execute(f"SELECT (SELECT COUNT(*) FROM users WHERE tenant_profile = ? AND id NOT IN {FX}) + "
                         f"(SELECT COUNT(*) FROM tenant_bindings WHERE profile_name = ? AND user_id NOT IN {FX})",
                         (slug, slug)).fetchone()[0]
        if n:
            raise Refused("a profile named in the manifest belongs to an account that is not a fixture")
    for table, pred in SHARED_REFS:
        n = count(conn, table, pred)
        if n:
            raise Refused(f"{n} row(s) in {table} outside the fixtures depend on a fixture; an operator must decide")


def freeze(conn, ids):
    """What an admin suspension does, for fixtures only: nothing new is claimed for provisioning and no
    session still works. Idempotent."""
    conn.execute("BEGIN IMMEDIATE")
    t = ts(now())
    conn.execute(f"UPDATE app_sessions SET revoked_at = ? WHERE revoked_at IS NULL AND user_id IN {FX}", (t,))
    conn.execute(f"UPDATE tenant_bindings SET status='DISABLED' WHERE user_id IN {FX} AND status != 'DISABLED'")
    conn.execute(f"UPDATE users SET approval_status='SUSPENDED', tenant_status='DISABLED' WHERE id IN {FX} "
                 f"AND google_issuer = ? AND (approval_status != 'SUSPENDED' OR tenant_status != 'DISABLED')",
                 (SYNTHETIC_ISSUER,))
    conn.execute("COMMIT")


def slugs(conn):
    out = set()
    for q in (f"SELECT tenant_profile FROM users WHERE id IN {FX}",
              f"SELECT profile_name FROM tenant_bindings WHERE user_id IN {FX}"):
        out.update(r[0] for r in conn.execute(q) if r[0])
    return out


def delete_rows(conn, manifest, manifest_path):
    """One transaction, foreign keys on. Returns {table: rows deleted}. Rolls back on any mismatch."""
    for _ in range(5):
        found = slugs(conn)
        if not found <= set(manifest["profiles"]):  # recorded BEFORE the rows go: a retry must still find them
            manifest["profiles"] = sorted(set(manifest["profiles"]) | found)
            write_json(manifest_path, manifest)
        conn.execute("BEGIN IMMEDIATE")
        try:
            if not slugs(conn) <= set(manifest["profiles"]):
                conn.execute("ROLLBACK")  # a claim stored a new slug between the read and the lock
                continue
            conn.execute("DELETE FROM fxa")
            conn.execute(f"INSERT INTO fxa SELECT id FROM audit_log WHERE actor_user_id IN {FX}")
            before = non_fixture_digest(conn)
            audit_total = conn.execute("SELECT COUNT(*) FROM audit_log").fetchone()[0]
            plan = {t: count(conn, t, OWNED[t]) for t in DELETE_ORDER}
            nulled = conn.execute("SELECT COUNT(*) FROM fxa").fetchone()[0]
            done = {}
            # Audit evidence is kept: its fixture actor goes first, so the delete of the users can proceed.
            cur = conn.execute(f"UPDATE audit_log SET actor_user_id = NULL WHERE actor_user_id IN {FX}")
            if cur.rowcount != nulled:
                raise CleanupError("audit_log: actor-null count differs from the plan")
            for t in DELETE_ORDER:
                try:
                    cur = conn.execute(f'DELETE FROM "{t}" WHERE COALESCE(({OWNED[t]}),0)=1')
                except sqlite3.IntegrityError:
                    raise CleanupError(f"{t}: a foreign key refused the delete; rolled back")
                if cur.rowcount != plan[t]:
                    raise CleanupError(f"{t}: deleted {cur.rowcount}, expected {plan[t]}")
                done[t] = cur.rowcount
            if conn.execute("SELECT COUNT(*) FROM audit_log").fetchone()[0] != audit_total:
                raise CleanupError("audit_log lost rows")
            if non_fixture_digest(conn) != before:
                raise CleanupError("a row outside the fixtures changed")
            if conn.execute("PRAGMA foreign_key_check").fetchall():
                raise CleanupError("foreign key violations after the delete")
            conn.execute("COMMIT")
            done["audit_log(actor nulled)"] = nulled
            return done
        except BaseException:
            if conn.in_transaction:
                conn.execute("ROLLBACK")
            raise
    raise CleanupError("provisioning kept changing the fixtures' profile names")


def residue(conn, ids, hermes, slugs_):
    """What is still there, as counts and short ids only."""
    left = {}
    for t in tables(conn):
        if t in ("admin_bootstrap", "model_blocked", "schema_migrations"):
            continue
        n = count(conn, t, OWNED[t] if t != "audit_log" else f"actor_user_id IN {FX}")
        if n:
            left[t] = n
    out = {"rows": left}
    live = hermes.live()
    out["profiles"] = sum(1 for s in slugs_ if s in live)
    return out


def residue_total(r):
    return sum(r["rows"].values()) + r["profiles"] + r.get("profilesFailed", 0)


def cleanup(db, manifest_path, hermes, recheck_seconds=30.0, recheck_interval=5.0):
    """Returns (exit code, report). Idempotent: a second run on a clean manifest changes nothing."""
    report = {"manifest": Path(manifest_path).name, "steps": [], "ok": False}
    step = lambda name, **kw: report["steps"].append({"step": name, **kw})
    try:
        manifest = load_manifest(manifest_path)
        ids = [f["id"] for f in manifest["fixtures"]]
        report["runId"] = manifest["runId"]
        conn = connect(db)
        try:
            load_fx(conn, ids)
            ic, fk = health(conn)
            step("integrity.before", integrityOk=ic, foreignKeyViolations=fk)
            if not ic or fk:
                raise CleanupError("database fails its integrity check before cleanup; nothing was changed")
            preflight(conn, manifest)
            present = conn.execute(f"SELECT COUNT(*) FROM users WHERE id IN {FX}").fetchone()[0]
            step("ownership", fixturesInManifest=len(ids), fixturesPresent=present)
            if present:
                backup = Path(manifest_path).parent / "pre-cleanup.sqlite"
                if not backup.exists():  # consistent snapshot, kept beside the manifest (0700 dir)
                    dst = sqlite3.connect(str(backup))
                    try:
                        conn.backup(dst)
                    finally:
                        dst.close()
                    os.chmod(backup, 0o600)
                step("backup", file="pre-cleanup.sqlite")
                freeze(conn, ids)
            deleted = delete_rows(conn, manifest, manifest_path)
            step("rows.deleted", **{k: v for k, v in deleted.items() if v})
            profiles = sorted(manifest["profiles"])
            failed = {s for s in profiles if not hermes.remove(s)}
            step("profiles.removed", profiles=len(profiles), failed=len(failed))
            deadline = time.monotonic() + recheck_seconds
            polls = 0
            while True:  # bounded window against a provision that was already in flight
                polls += 1
                live = hermes.live()
                for s in profiles:
                    if s in live or s in failed:
                        failed.discard(s) if hermes.remove(s) else failed.add(s)
                r = residue(conn, ids, hermes, profiles)
                if time.monotonic() >= deadline:
                    break
                time.sleep(min(recheck_interval, max(0.0, deadline - time.monotonic())))
            r["profilesFailed"] = len(failed)
            step("recheck", polls=polls, windowSeconds=recheck_seconds, residue=r)
            ic, fk = health(conn)
            step("integrity.after", integrityOk=ic, foreignKeyViolations=fk)
            if residue_total(r) or not ic or fk:
                report["residual"] = r
                report["next"] = f"fix the cause, then re-run: smoke_fixtures.py cleanup --db <db> --manifest {manifest_path}"
                write_json(manifest_path, {**manifest, "state": "cleanup-failed"})
                return CLEANUP_FAILED, report
        finally:
            conn.close()
        write_json(manifest_path, {**manifest, "state": "cleaned"})
        report["ok"] = True
        return OK, report
    except Refused as e:
        report["refused"] = str(e)
        return REFUSED, report
    except (CleanupError, sqlite3.Error, OSError) as e:
        report["error"] = f"{type(e).__name__}: {e}" if isinstance(e, CleanupError) else type(e).__name__
        report["next"] = f"fix the cause, then re-run: smoke_fixtures.py cleanup --db <db> --manifest {manifest_path}"
        return CLEANUP_FAILED, report



# ---- run --------------------------------------------------------------------------------------------------

class Interrupted(BaseException):
    pass


def _raise_interrupted(signum, _frame):
    raise Interrupted(signal.Signals(signum).name)


def stop_group(proc):
    """End every process of the smoke's group, whether or not its leader is still alive: a wrapper that exited
    or died on SIGTERM can leave children that keep provisioning after the cleanup."""
    def members():
        proc.poll()  # reap the leader, or a zombie keeps the group looking alive
        try:
            os.killpg(proc.pid, 0)
            return True
        except ProcessLookupError:
            return False
    for sig, grace in ((signal.SIGTERM, 10.0), (signal.SIGKILL, 10.0)):
        if not members():
            return
        try:
            os.killpg(proc.pid, sig)
        except ProcessLookupError:
            return
        end = time.monotonic() + grace
        while members() and time.monotonic() < end:
            time.sleep(0.05)


def leftover_manifests(workdir):
    for p in sorted(Path(workdir).glob("run-*/manifest.json")):
        try:
            if json.loads(p.read_text()).get("state") != "cleaned":
                yield p
        except (OSError, ValueError):
            yield p


def run(args, smoke_argv):
    hermes = Hermes(shlex.split(args.hermes_cmd))
    workdir = Path(args.workdir)
    workdir.mkdir(mode=0o700, parents=True, exist_ok=True)
    for old in list(leftover_manifests(workdir)):  # a run that could not clean up (SIGKILL, power loss)
        code, rep = cleanup(args.db, old, hermes, args.recheck, args.recheck_interval)
        print(f"smoke_fixtures: leftover {old.parent.name}: exit {code}", file=sys.stderr)
        if code != OK:
            print(json.dumps(rep, indent=2), file=sys.stderr)
            print("smoke_fixtures: refusing to start while an earlier run's fixtures are still present", file=sys.stderr)
            return code
    path, manifest = plan_manifest(workdir)
    result = {"runId": manifest["runId"], "smokeExit": None, "smoke": None, "cleanup": None}
    old = {s: signal.signal(s, _raise_interrupted) for s in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP)}
    proc = None
    try:
        cookies = mint(args.db, path, manifest)
        env = dict(os.environ)
        for role, name in (("admin", "ADMIN"), ("a", "A"), ("b", "B"), ("pending", "PENDING")):
            env[f"ANTARES_SMOKE_COOKIE_{name}"] = cookies[role]
        proc = subprocess.Popen(smoke_argv, env=env, start_new_session=True)
        try:
            result["smokeExit"] = proc.wait(timeout=args.timeout)
            result["smoke"] = "pass" if result["smokeExit"] == 0 else "fail"
        except subprocess.TimeoutExpired:
            result["smoke"], result["smokeExit"] = "timeout", None
    except Interrupted as e:
        result["smoke"] = f"interrupted ({e})"
    except BaseException as e:  # mint failed, or the smoke could not start
        result["smoke"] = f"error ({type(e).__name__})"
    finally:
        for s in old:  # cleanup must not be cut short by a second signal
            signal.signal(s, signal.SIG_IGN)
        if proc is not None:
            stop_group(proc)
        code, rep = cleanup(args.db, path, hermes, args.recheck, args.recheck_interval)
        result["cleanup"] = rep
        for s, h in old.items():
            signal.signal(s, h)
    result["verdict"] = "PASS" if code == OK and result["smokeExit"] == 0 else "FAIL"
    result["exit"] = CLEANUP_FAILED if code != OK else (OK if result["smokeExit"] == 0 else SMOKE_FAILED)
    if code == REFUSED:
        result["exit"] = REFUSED
    out = Path(args.result) if args.result else path.parent / "result.json"
    write_json(out, result)
    print(f"smoke_fixtures: smoke={result['smoke']} cleanup={'clean' if code == OK else 'NOT CLEAN'} "
          f"verdict={result['verdict']} result={out}", file=sys.stderr)
    if code != OK:
        print(json.dumps({k: rep.get(k) for k in ("refused", "error", "residual", "next")}, indent=2), file=sys.stderr)
    return result["exit"]


def main(argv):
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    sub = ap.add_subparsers(dest="cmd", required=True)
    for name in ("run", "mint", "cleanup"):
        p = sub.add_parser(name)
        p.add_argument("--db", required=True)
        p.add_argument("--hermes-cmd", default=os.environ.get("ANTARES_FIXTURES_HERMES_CMD", "docker exec -u hermes hermes hermes"),
                       help="argv prefix that runs the hermes CLI in the Hermes container, as the hermes user (never root)")
        p.add_argument("--recheck", type=float, default=30.0, help="seconds to watch for a late reprovision")
        p.add_argument("--recheck-interval", type=float, default=5.0)
        if name == "cleanup":
            p.add_argument("--manifest", required=True)
        else:
            p.add_argument("--workdir", required=True)
        if name == "run":
            p.add_argument("--timeout", type=float, default=1500.0, help="bound on the smoke command, seconds")
            p.add_argument("--result", help="where to write the run result JSON (default: beside the manifest)")
    args, rest = ap.parse_known_args(argv)
    if args.cmd == "run":
        if rest[:1] == ["--"]:
            rest = rest[1:]
        if not rest:
            ap.error("run needs the smoke command after --")
        return run(args, rest)
    if rest:
        ap.error("unexpected arguments")
    if args.cmd == "mint":
        path, manifest = plan_manifest(args.workdir)
        cookies = mint(args.db, path, manifest)
        env = path.parent / "smoke.env"
        fd = os.open(env, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w") as f:
            for role, name in (("admin", "ADMIN"), ("a", "A"), ("b", "B"), ("pending", "PENDING")):
                f.write(f"ANTARES_SMOKE_COOKIE_{name}={cookies[role]}\n")
        print(f"manifest {path}\nsessions {env} (0600; delete it with the cleanup)")
        return OK
    code, rep = cleanup(args.db, args.manifest, Hermes(shlex.split(args.hermes_cmd)), args.recheck, args.recheck_interval)
    print(json.dumps(rep, indent=2))
    if code == OK:
        (Path(args.manifest).parent / "smoke.env").unlink(missing_ok=True)
    return code


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

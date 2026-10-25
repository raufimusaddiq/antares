"""Offline tests for smoke_fixtures.py (#144): the real migrations build the database, a fake `hermes` CLI keeps
profiles in a JSON file, and a fake smoke command provisions fixtures the way the tenant sweep does. No network,
no Hermes, no real account."""
import contextlib, hashlib, io, json, os, signal, sqlite3, subprocess, sys, tempfile, textwrap, time, unittest
from pathlib import Path

import smoke_fixtures as sf

ROOT = Path(__file__).resolve().parent.parent
SCRIPT = ROOT / "scripts" / "smoke_fixtures.py"
REAL_ISSUER = "https://accounts.google.com"

FAKE_HERMES = textwrap.dedent('''
    import json, sys
    state_path, args = sys.argv[1], sys.argv[2:]
    st = json.load(open(state_path))
    def save(): json.dump(st, open(state_path, "w"))
    if args[:2] == ["profile", "list"]:
        print("  Profile   Model"); print("◆default  x")
        for p in st["profiles"]: print(f"  {p}  x")
    elif args[:2] == ["profile", "delete"]:
        slug = args[2]
        if slug in st.get("stuck", []): sys.exit(1)
        if slug in st["profiles"]: st["profiles"].remove(slug)
        if st.get("reappear", {}).get(slug, 0) > 0:        # a provision that was already in flight
            st["reappear"][slug] -= 1; st["profiles"].append(slug)
        save()
    elif args[:2] == ["profile", "purge-identity"]:
        slug = args[2]
        if st.get("fail_purge", {}).get(slug, 0) > 0:
            st["fail_purge"][slug] -= 1; save(); sys.exit(1)
    else:
        sys.exit(2)
''')

# Stands in for antares-smoke: finds its own account by the cookie it was given, then does what the tenant sweep
# and a chat do (a profile, a binding, a chat, a run, a search doc, an audit row).
FAKE_SMOKE = textwrap.dedent('''
    import hashlib, json, os, sqlite3, sys, time, uuid
    mode, db, state = sys.argv[1:4]
    conn = sqlite3.connect(db, timeout=30, isolation_level=None); conn.execute("PRAGMA foreign_keys=ON")
    def uid(name):
        h = hashlib.sha256(os.environ["ANTARES_SMOKE_COOKIE_" + name].encode()).hexdigest()
        return conn.execute("SELECT user_id FROM app_sessions WHERE token_hash=?", (h,)).fetchone()[0]
    st = json.load(open(state))
    for name in ("ADMIN", "A", "B"):
        u, slug, i = uid(name), "u_" + uuid.uuid4().hex[:12], uuid.uuid4().hex
        t = "2026-10-25T00:00:00.000000000Z"
        conn.execute("UPDATE users SET tenant_profile=?, tenant_status='READY' WHERE id=?", (slug, u))
        conn.execute("INSERT INTO tenant_bindings (id,user_id,profile_name,routing_mode,upstream_endpoint_id,credential_ciphertext,credential_key_version,credential_fingerprint,memory_scope_key,status,created_at) VALUES (?,?,?, 'MUX_PREFIX','e',x'00',1,?,?, 'READY',?)",
                     (i, u, slug, uuid.uuid4().hex, uuid.uuid4().hex, t))
        conn.execute("INSERT INTO chats (id,user_id,hermes_binding_id,hermes_session_id,title,status,created_at,updated_at) VALUES (?,?,?,?,?, 'ACTIVE',?,?)", (i + "c", u, i, "s" + i, None, t, t))
        conn.execute("INSERT INTO runs (id,user_id,chat_id,idempotency_key,status,created_at,updated_at) VALUES (?,?,?,?, 'COMPLETED',?,?)",
                     (i + "r", u, i + "c", "k", t, t))
        conn.execute("INSERT INTO search_docs (user_id,chat_id,seq,role,body,folded) VALUES (?,?,0,'user','b','b')", (u, i + "c"))
        conn.execute("INSERT INTO audit_log (id,actor_user_id,action,outcome,created_at) VALUES (?,?, 'x','ok',?)", (i + "a", u, t))
        st["profiles"].append(slug)
    admin = uid("ADMIN")
    conn.execute("INSERT INTO approvals (id,target_user_id,actor_user_id,action,created_at) VALUES (?,?,?, 'approve',?)",
                 (uuid.uuid4().hex, uid("A"), admin, "2026-10-25T00:00:00.000000000Z"))
    json.dump(st, open(state, "w"))
    if mode == "sleep":
        open(state + ".started", "w").close(); time.sleep(120)
    sys.exit({"pass": 0, "fail": 1}.get(mode, 0))
''')


def build_db(path):
    conn = sqlite3.connect(path)
    for f in sorted((ROOT / "internal" / "store" / "migrations").glob("*.sql")):
        conn.executescript(f.read_text())
    conn.execute("CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT, checksum TEXT, applied_at TEXT)")
    t = "2026-10-20T00:00:00.000000000Z"
    user = lambda i, issuer, sub, role, email: conn.execute(
        "INSERT INTO users (id,google_issuer,google_sub,email,display_name,role,approval_status,tenant_status,created_at,tenant_profile)"
        " VALUES (?,?,?,?,?,?, 'APPROVED','READY',?,?)", (i, issuer, sub, email, "Smoke A", role, t, "u_real" + i[:8]))
    user("0" * 32, REAL_ISSUER, "owner-sub", "ADMIN", "owner@example.com")
    # A decoy that looks like a fixture by name and email, and a synthetic account of another run: neither is ours.
    user("1" * 32, REAL_ISSUER, "decoy", "USER", "smoke-a@synthetic-test.invalid")
    conn.execute("INSERT INTO users (id,google_issuer,google_sub,email,role,approval_status,tenant_status,created_at)"
                 " VALUES (?,?, 'bare', 'bare@example.com', 'USER', 'PENDING', 'UNPROVISIONED', ?)", ("3" * 32, REAL_ISSUER, t))
    user("2" * 32, sf.SYNTHETIC_ISSUER, "antares-smoke-aaaaaaaaaaaa-a", "USER", "other@synthetic-test.invalid")
    conn.execute("INSERT INTO admin_bootstrap (id,user_id,created_at) VALUES (1, ?, ?)", ("0" * 32, t))
    for u in ("0" * 32, "1" * 32):
        conn.execute("INSERT INTO tenant_bindings (id,user_id,profile_name,routing_mode,upstream_endpoint_id,credential_ciphertext,credential_key_version,credential_fingerprint,memory_scope_key,status,created_at) VALUES (?,?,?, 'MUX_PREFIX','e',x'00',1,?,?, 'READY',?)",
                     ("b" + u[:8], u, "u_real" + u[:8], "fp" + u[:8], "ms" + u[:8], t))
        conn.execute("INSERT INTO chats (id,user_id,hermes_binding_id,hermes_session_id,title,status,created_at,updated_at) VALUES (?,?,?,?,?, 'ACTIVE',?,?)", ("c" + u[:8], u, "b" + u[:8], "s" + u[:8], "t", t, t))
        conn.execute("INSERT INTO search_docs (user_id,chat_id,seq,role,body,folded) VALUES (?,?,0,'user','real words','real words')", (u, "c" + u[:8]))
    conn.execute("INSERT INTO approvals (id,target_user_id,actor_user_id,action,created_at) VALUES ('ap1', ?, ?, 'approve',?)", ("1" * 32, "0" * 32, t))
    conn.execute("INSERT INTO audit_log (id,actor_user_id,action,outcome,created_at) VALUES ('au1', ?, 'login','ok',?)", ("0" * 32, t))
    conn.execute("INSERT INTO app_sessions (id,user_id,token_hash,created_at,expires_at) VALUES ('s1', ?, 'realhash', ?, '2099-01-01T00:00:00.000000000Z')", ("0" * 32, t))
    conn.commit()
    conn.close()


def dump(path):
    conn = sqlite3.connect(path)
    out = {}
    for (t,) in conn.execute("SELECT name FROM sqlite_master WHERE type='table'").fetchall():
        out[t] = {repr(r) for r in conn.execute(f'SELECT * FROM "{t}"')}
    conn.close()
    return out


class Env(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        d = Path(self.tmp.name)
        self.db, self.work, self.state = str(d / "a.sqlite"), str(d / "work"), str(d / "hermes.json")
        (d / "hermes.py").write_text(FAKE_HERMES)
        (d / "smoke.py").write_text(FAKE_SMOKE)
        self.hermes = [sys.executable, str(d / "hermes.py"), self.state]
        self.smoke = [sys.executable, str(d / "smoke.py")]
        build_db(self.db)
        self.set_state({"profiles": ["u_real00000000", "u_real11111111"]})
        self.seed = dump(self.db)

    def set_state(self, st):
        Path(self.state).write_text(json.dumps(st))

    def get_state(self):
        return json.loads(Path(self.state).read_text())

    def args(self, cmd, *extra, recheck="0"):
        return [cmd, "--db", self.db, "--hermes-cmd", " ".join(self.hermes), "--recheck", recheck, "--recheck-interval", "0", *extra]

    def main(self, argv):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = sf.main(argv)
        return code, out.getvalue() + err.getvalue()

    def run_smoke(self, mode, *extra, recheck="0"):
        return self.main(self.args("run", "--workdir", self.work, *extra, recheck=recheck) + ["--", *self.smoke, mode, self.db, self.state])

    def manifest(self):
        ms = sorted(Path(self.work).glob("run-*/manifest.json"))
        self.assertEqual(len(ms), 1)
        return ms[0], json.loads(ms[0].read_text())

    def assertOnlySeedLeft(self):
        after = dump(self.db)
        for t, rows in self.seed.items():
            if t == "audit_log":
                self.assertTrue(rows <= after[t], "an audit row was lost or changed")
                for r in after[t] - rows:  # kept evidence of fixtures: actor nulled
                    self.assertIn(", None, ", r[:80])
            else:
                self.assertEqual(after[t], rows, f"{t} differs from the seed")
        conn = sqlite3.connect(self.db)
        self.assertEqual(conn.execute("PRAGMA integrity_check").fetchone()[0], "ok")
        self.assertEqual(conn.execute("PRAGMA foreign_key_check").fetchall(), [])
        self.assertEqual(self.get_state()["profiles"], ["u_real00000000", "u_real11111111"])


class Run(Env):
    def test_success_cleans_everything_and_is_a_pass(self):
        code, out = self.run_smoke("pass")
        self.assertEqual(code, 0, out)
        self.assertOnlySeedLeft()
        path, m = self.manifest()
        self.assertEqual(m["state"], "cleaned")
        res = json.loads((path.parent / "result.json").read_text())
        self.assertEqual((res["verdict"], res["smoke"], res["cleanup"]["ok"]), ("PASS", "pass", True))
        deleted = next(s for s in res["cleanup"]["steps"] if s["step"] == "rows.deleted")
        self.assertEqual((deleted["users"], deleted["tenant_bindings"], deleted["app_sessions"]), (4, 3, 4))
        self.assertEqual(deleted["audit_log(actor nulled)"], 3)

    def test_failing_smoke_still_cleans_and_keeps_its_exit_code(self):
        code, out = self.run_smoke("fail")
        self.assertEqual(code, 1, out)
        self.assertOnlySeedLeft()
        self.assertEqual(json.loads((self.manifest()[0].parent / "result.json").read_text())["verdict"], "FAIL")

    def test_timeout_kills_the_smoke_and_cleans(self):
        code, out = self.run_smoke("sleep", "--timeout", "3")
        self.assertEqual(code, 1, out)
        self.assertOnlySeedLeft()
        self.assertEqual(json.loads((self.manifest()[0].parent / "result.json").read_text())["smoke"], "timeout")

    def test_manifest_exists_before_the_first_fixture_row(self):
        seen = {}
        real = sf.mint

        def spy(db, path, manifest):
            seen["manifest"] = json.loads(Path(path).read_text())
            seen["users"] = sqlite3.connect(db).execute("SELECT COUNT(*) FROM users WHERE google_issuer=?", (sf.SYNTHETIC_ISSUER,)).fetchone()[0]
            return real(db, path, manifest)
        sf.mint = spy
        self.addCleanup(setattr, sf, "mint", real)
        self.assertEqual(self.run_smoke("pass")[0], 0)
        self.assertEqual(seen["users"], 1)  # only the other run's account; none of ours yet
        self.assertEqual(seen["manifest"]["state"], "planned")
        self.assertEqual(len(seen["manifest"]["fixtures"]), 4)

    def test_a_crash_while_minting_still_leaves_a_manifest_and_cleans(self):
        real = sf.mint

        def boom(db, path, manifest):
            raise sqlite3.OperationalError("disk I/O error")
        sf.mint = boom
        self.addCleanup(setattr, sf, "mint", real)
        code, out = self.run_smoke("pass")
        self.assertEqual(code, 1, out)  # no smoke ran, so no pass
        self.assertEqual(self.manifest()[1]["state"], "cleaned")
        self.assertOnlySeedLeft()

    def test_no_secret_or_user_data_in_output_or_report(self):
        cookies = {}
        real = sf.mint

        def spy(db, path, manifest):
            cookies.update(real(db, path, manifest))
            return cookies
        sf.mint = spy
        self.addCleanup(setattr, sf, "mint", real)
        code, out = self.run_smoke("pass")
        blob = out + (self.manifest()[0].parent / "result.json").read_text() + self.manifest()[0].read_text()
        self.assertEqual(code, 0)
        for v in cookies.values():
            self.assertNotIn(v, blob)
        for word in ("@synthetic-test.invalid", "owner@example.com", "u_real"):
            self.assertNotIn(word, blob)


class Interruption(Env):
    def run_and_signal(self, sig):
        p = subprocess.Popen([sys.executable, "-I", str(SCRIPT), *self.args("run", "--workdir", self.work), "--", *self.smoke, "sleep", self.db, self.state],
                             stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        for _ in range(200):
            if Path(self.state + ".started").exists():
                break
            time.sleep(0.1)
        else:
            p.kill()
            self.fail("the fake smoke never started")
        p.send_signal(sig)
        out, err = p.communicate(timeout=60)
        return p.returncode, out + err

    def test_sigterm_cleans_up_and_is_not_a_pass(self):
        code, out = self.run_and_signal(signal.SIGTERM)
        self.assertEqual(code, 1, out)
        self.assertOnlySeedLeft()
        self.assertEqual(self.manifest()[1]["state"], "cleaned")

    def test_sigint_cleans_up_and_is_not_a_pass(self):
        code, out = self.run_and_signal(signal.SIGINT)
        self.assertEqual(code, 1, out)
        self.assertOnlySeedLeft()

    def test_an_uncatchable_death_is_cleaned_by_the_next_run_which_refuses_to_start_otherwise(self):
        path, m = sf.plan_manifest(self.work)
        sf.mint(self.db, path, m)  # as if the run was SIGKILLed right after minting
        conn = sqlite3.connect(self.db)
        self.assertEqual(conn.execute("SELECT COUNT(*) FROM users WHERE google_sub LIKE ?", (f"antares-smoke-{m['runId']}-%",)).fetchone()[0], 4)
        code, out = self.run_smoke("pass")
        self.assertEqual(code, 0, out)
        self.assertOnlySeedLeft()
        self.assertEqual({json.loads(p.read_text())["state"] for p in Path(self.work).glob("run-*/manifest.json")}, {"cleaned"})


class Partial(Env):
    def test_profile_deletion_failure_is_not_success_then_retry_completes(self):
        st = self.get_state()
        st["stuck"] = []
        self.set_state(st)
        # the smoke creates three profiles; make the first purge-identity of any of them fail by pre-seeding via mint+provision
        path, m = sf.plan_manifest(self.work)
        sf.mint(self.db, path, m)
        slug = "u_stuck0000001"
        conn = sqlite3.connect(self.db)
        uid = m["fixtures"][1]["id"]
        conn.execute("UPDATE users SET tenant_profile=? WHERE id=?", (slug, uid))
        conn.commit()
        self.set_state({"profiles": ["u_real00000000", "u_real11111111", slug], "stuck": [slug]})
        code, out = self.main(self.args("cleanup", "--manifest", str(path)))
        self.assertEqual(code, 4, out)
        report = json.loads(out)
        self.assertEqual(report["residual"]["profiles"], 1)
        self.assertIn("re-run", report["next"])
        self.assertNotIn(slug, out)
        self.assertEqual(json.loads(path.read_text())["state"], "cleanup-failed")
        self.assertEqual(json.loads(path.read_text())["profiles"], [slug])  # recorded before the rows went
        conn = sqlite3.connect(self.db)
        self.assertEqual(conn.execute("SELECT COUNT(*) FROM users WHERE id IN (%s)" % ",".join("?" * 4), [f["id"] for f in m["fixtures"]]).fetchone()[0], 0)
        # retry once the profile can be removed: rows are already gone, only the profile is left
        self.set_state({"profiles": ["u_real00000000", "u_real11111111", slug]})
        code, out = self.main(self.args("cleanup", "--manifest", str(path)))
        self.assertEqual(code, 0, out)
        self.assertEqual(self.get_state()["profiles"], ["u_real00000000", "u_real11111111"])
        self.assertEqual(json.loads(path.read_text())["state"], "cleaned")

    def test_a_purge_that_fails_once_is_retried_inside_the_window(self):
        path, m = sf.plan_manifest(self.work)
        sf.mint(self.db, path, m)
        slug = "u_flaky0000001"
        conn = sqlite3.connect(self.db)
        conn.execute("UPDATE users SET tenant_profile=? WHERE id=?", (slug, m["fixtures"][0]["id"]))
        conn.commit()
        self.set_state({"profiles": [slug], "fail_purge": {slug: 2}})  # the first attempt and the one re-check poll
        self.assertEqual(self.main(self.args("cleanup", "--manifest", str(path), recheck="0"))[0], 4)
        self.set_state({"profiles": [slug], "fail_purge": {slug: 1}})  # the re-check poll succeeds
        code, out = self.main(self.args("cleanup", "--manifest", str(path), recheck="0"))
        self.assertEqual(code, 0, out)

    def test_profiles_survive_a_crash_between_the_commit_and_their_removal(self):
        path, m = sf.plan_manifest(self.work)
        sf.mint(self.db, path, m)
        slug = "u_crash0000001"
        conn = sqlite3.connect(self.db)
        conn.execute("UPDATE users SET tenant_profile=? WHERE id=?", (slug, m["fixtures"][0]["id"]))
        conn.commit()
        self.set_state({"profiles": [slug]})
        real = sf.Hermes.remove

        def die(self_, s):
            raise RuntimeError("process died")
        sf.Hermes.remove = die
        try:
            with self.assertRaises(RuntimeError):
                self.main(self.args("cleanup", "--manifest", str(path)))
        finally:
            sf.Hermes.remove = real
        self.assertEqual(conn.execute("SELECT COUNT(*) FROM users WHERE tenant_profile=?", (slug,)).fetchone()[0], 0)
        self.assertEqual(self.get_state()["profiles"], [slug])  # rows are gone; only the manifest still knows the slug
        self.assertEqual(self.main(self.args("cleanup", "--manifest", str(path)))[0], 0)
        self.assertEqual(self.get_state()["profiles"], [])

    def test_cleanup_twice_is_a_no_op(self):
        path, m = sf.plan_manifest(self.work)
        sf.mint(self.db, path, m)
        self.assertEqual(self.main(self.args("cleanup", "--manifest", str(path)))[0], 0)
        first = dump(self.db)
        self.assertEqual(self.main(self.args("cleanup", "--manifest", str(path)))[0], 0)
        self.assertEqual(dump(self.db), first)

    def test_late_reprovision_inside_the_window_is_removed_again(self):
        path, m = sf.plan_manifest(self.work)
        sf.mint(self.db, path, m)
        slug = "u_late00000001"
        conn = sqlite3.connect(self.db)
        conn.execute("UPDATE users SET tenant_profile=? WHERE id=?", (slug, m["fixtures"][0]["id"]))
        conn.commit()
        self.set_state({"profiles": [slug], "reappear": {slug: 1}})
        code, out = self.main(self.args("cleanup", "--manifest", str(path), recheck="0.3"))
        self.assertEqual(code, 0, out)
        self.assertEqual(self.get_state()["profiles"], [])

    def test_a_profile_that_keeps_coming_back_is_a_failure(self):
        path, m = sf.plan_manifest(self.work)
        sf.mint(self.db, path, m)
        slug = "u_late00000002"
        conn = sqlite3.connect(self.db)
        conn.execute("UPDATE users SET tenant_profile=? WHERE id=?", (slug, m["fixtures"][0]["id"]))
        conn.commit()
        self.set_state({"profiles": [slug], "reappear": {slug: 1000}})
        code, out = self.main(self.args("cleanup", "--manifest", str(path), recheck="0.3"))
        self.assertEqual(code, 4, out)
        self.assertGreaterEqual(json.loads(out)["residual"]["profiles"], 1)


class Refusal(Env):
    def manifest_with(self, extra_fixture=None, profiles=()):
        path, m = sf.plan_manifest(self.work)
        sf.mint(self.db, path, m)
        if extra_fixture:
            m["fixtures"].append(extra_fixture)
        m["profiles"] = list(profiles)
        sf.write_json(path, m)
        return path, m

    def assertRefused(self, path, before):
        code, out = self.main(self.args("cleanup", "--manifest", str(path)))
        self.assertEqual(code, 5, out)
        self.assertEqual(dump(self.db), before, "a refused cleanup must change nothing")
        self.assertEqual(self.get_state()["profiles"], ["u_real00000000", "u_real11111111"])

    def test_refuses_a_real_account_listed_in_the_manifest(self):
        for victim in ("0" * 32, "1" * 32, "3" * 32):  # the owner admin; a look-alike; a real account with no other ties
            with self.subTest(victim=victim[:2]):
                path, m = self.manifest_with()
                m["fixtures"].append({"role": "a", "id": victim, "sub": f"antares-smoke-{m['runId']}-a"})
                sf.write_json(path, m)
                self.assertRefused(path, dump(self.db))

    def test_refuses_a_synthetic_issuer_account_that_is_not_in_this_manifest_by_sub(self):
        path, m = self.manifest_with()
        # synthetic issuer, but the stored sub belongs to another run, so the manifest does not own it
        m["fixtures"].append({"role": "a", "id": "2" * 32, "sub": f"antares-smoke-{m['runId']}-a"})
        sf.write_json(path, m)
        self.assertRefused(path, dump(self.db))

    def test_refuses_a_manifest_for_another_issuer_or_with_bad_ids(self):
        path, m = self.manifest_with()
        before = dump(self.db)
        for mutate in (lambda d: d.update(issuer=REAL_ISSUER), lambda d: d["fixtures"][0].update(id="0" * 32 + "x"),
                       lambda d: d.update(profiles=["default"]), lambda d: d.update(profiles=["../u_abcdefgh"]),
                       lambda d: d.update(version=2)):
            d = json.loads(path.read_text())
            mutate(d)
            sf.write_json(path, d)
            self.assertRefused(path, before)

    def test_refuses_a_profile_of_a_real_account_named_in_the_manifest(self):
        path, _ = self.manifest_with(profiles=["u_real00000000"])
        # the owner's tenant_profile is u_real + id[:8] = u_real00000000
        self.assertRefused(path, dump(self.db))

    def test_refuses_when_a_non_fixture_row_depends_on_a_fixture(self):
        path, m = self.manifest_with()
        conn = sqlite3.connect(self.db)
        conn.execute("INSERT INTO approvals (id,target_user_id,actor_user_id,action,created_at) VALUES ('mix', ?, ?, 'approve','2026-10-25T00:00:00.000000000Z')",
                     (m["fixtures"][1]["id"], "0" * 32))  # the real admin approved a fixture: evidence of a real actor
        conn.commit()
        self.assertRefused(path, dump(self.db))

    def test_refuses_the_original_admin_latch(self):
        path, m = self.manifest_with()
        conn = sqlite3.connect(self.db)
        conn.execute("UPDATE admin_bootstrap SET user_id=?", (m["fixtures"][0]["id"],))
        conn.commit()
        self.assertRefused(path, dump(self.db))

    def test_refuses_a_table_it_does_not_know(self):
        path, _ = self.manifest_with()
        conn = sqlite3.connect(self.db)
        conn.execute("CREATE TABLE future (user_id TEXT REFERENCES users(id))")
        conn.commit()
        self.assertRefused(path, dump(self.db))

    def test_leaves_other_synthetic_accounts_and_name_lookalikes_alone(self):
        self.assertEqual(self.run_smoke("pass")[0], 0)
        self.assertOnlySeedLeft()  # includes the decoy and the other run's synthetic account
        conn = sqlite3.connect(self.db)
        self.assertEqual(conn.execute("SELECT COUNT(*) FROM users WHERE id IN (?,?)", ("1" * 32, "2" * 32)).fetchone()[0], 2)

    def test_every_table_in_the_migrations_is_classified(self):
        conn = sqlite3.connect(self.db)
        names = {r[0] for r in conn.execute("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")}
        self.assertEqual(names - set(sf.OWNED), set())
        self.assertEqual({t for t in sf.OWNED if t not in names}, set())


if __name__ == "__main__":
    unittest.main()

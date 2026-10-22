"""Offline self-test for the contract guard: no network, no Hermes."""
import copy, json, os, tempfile, unittest
from pathlib import Path
from unittest import mock

import hermes_contract as hc

TOOLSETS = {"data": [
    {"name": "web", "enabled": True, "tools": ["web_search"]},
    {"name": "browser", "enabled": False, "tools": ["browser_click"]},
]}


def check(live, fixture, name="toolsets"):
    return hc.compare(name, 200, live, 200, hc.sanitize(fixture, hc.safe_for(name)))


class Contract(unittest.TestCase):
    def test_identical_passes(self):
        self.assertEqual(check(TOOLSETS, TOOLSETS), [])

    def test_values_are_ignored(self):
        live = copy.deepcopy(TOOLSETS)
        live["data"][0]["tools"] = ["other_name"]
        self.assertEqual(check(live, TOOLSETS), [])

    def test_toolset_added_is_caught(self):
        live = copy.deepcopy(TOOLSETS)
        live["data"].append({"name": "computer_use", "enabled": True, "tools": ["x"]})
        self.assertIn("computer_use: added (enabled=True)", check(live, TOOLSETS)[0])

    def test_toolset_removed_is_caught(self):
        live = copy.deepcopy(TOOLSETS)
        live["data"].pop(1)
        self.assertIn("browser: removed", check(live, TOOLSETS)[0])

    def test_toolset_swap_same_shape_is_caught(self):
        live = copy.deepcopy(TOOLSETS)
        live["data"][1]["name"] = "computer_use"
        live["data"][1]["enabled"] = True
        self.assertTrue(check(live, TOOLSETS))

    def test_toolset_newly_enabled_is_caught(self):
        live = copy.deepcopy(TOOLSETS)
        live["data"][1]["enabled"] = True
        self.assertEqual(check(live, TOOLSETS), ["toolsets: browser: False -> True"])

    def test_list_item_type_change_is_caught(self):
        live = copy.deepcopy(TOOLSETS)
        live["data"][1]["tools"] = [1]
        self.assertTrue(check(live, TOOLSETS))

    def test_key_added_removed_and_type_change_are_caught(self):
        for mutate in (lambda d: d.update(x=1), lambda d: d.pop("object"), lambda d: d.update(object=3)):
            live = {"object": "list"}
            mutate(live)
            self.assertTrue(check(live, {"object": "list"}, "models"))

    def test_status_change_is_caught(self):
        self.assertTrue(hc.compare("models", 500, {}, 200, {}))

    def test_free_text_is_redacted_by_default(self):
        body = {"title": "my tax return", "content": "/home/alice/secret.txt", "name": "alice",
                "status": "ok", "tools": ["terminal"]}
        self.assertEqual(hc.sanitize(body, hc.safe_for("sessions")),
                         {"title": "<redacted>", "content": "<redacted>", "name": "<redacted>",
                          "status": "ok", "tools": ["<redacted>"]})

    def test_name_is_kept_only_where_it_is_a_toolset_name(self):
        self.assertEqual(hc.sanitize({"name": "web"}, hc.safe_for("toolsets")), {"name": "web"})
        self.assertEqual(hc.sanitize({"name": "web"}, hc.safe_for("models")), {"name": "<redacted>"})

    def test_default_cases_do_not_pin_the_transient_probe(self):
        self.assertFalse([c for c in hc.CASES if hc.TRANSIENT_PROBE.match(c[2])])

    def test_transient_probe_matcher_is_exact(self):
        self.assertTrue(hc.TRANSIENT_PROBE.match("/p/testa/v1/models"))
        self.assertFalse(hc.TRANSIENT_PROBE.match("/p/testa/v1/toolsets"))

    def test_tenant_toolsets_case_would_be_allowed(self):
        cases = list(hc.CASES) + [("toolsets-profile", "GET", "/p/testa/v1/toolsets", "owner")]
        self.assertFalse([c for c in cases if hc.TRANSIENT_PROBE.match(c[2])])

    def test_path_and_host_are_redacted_outside_their_cases(self):
        body = {"artifact": {"path": "/home/alice/2026-tax-return.pdf"}, "host": "10.1.2.3"}
        self.assertEqual(hc.sanitize(body, hc.safe_for("sessions")),
                         {"artifact": {"path": "<redacted>"}, "host": "<redacted>"})
        self.assertEqual(hc.sanitize({"path": "/v1/models"}, hc.safe_for("capabilities")), {"path": "/v1/models"})
        self.assertEqual(hc.sanitize({"host": "0.0.0.0"}, hc.safe_for("health-detailed")), {"host": "0.0.0.0"})
        self.assertEqual(hc.sanitize({"host": "0.0.0.0"}, hc.safe_for("capabilities")), {"host": "<redacted>"})

    def test_sanitize_fabricates_numbers_dates_and_urls(self):
        out = hc.sanitize({"n": 123456, "f": 9.5, "day": "2026-10-22", "ts": "2026-10-22T04:00:00+07:00",
                           "u": "http://172.1.2.3:8642", "ok": True})
        self.assertEqual(out, {"n": 1, "f": 1.0, "day": "2026-01-01", "ts": "2026-01-01T00:00:00+00:00",
                               "u": "http://host.example:8642", "ok": True})

    def test_int_float_and_other_type_changes_are_caught(self):
        for live in ({"created": 1.5}, {"created": None}, {"created": True}, {"created": "1"}):
            self.assertTrue(check(live, {"created": 1}, "models"), live)
        self.assertEqual(check({"created": 7}, {"created": 1}, "models"), [])

    def test_parse(self):
        self.assertEqual(hc.parse(b'{"a": 1}'), {"a": 1})
        self.assertEqual(hc.parse(b"not json"), "not json")


class Main(unittest.TestCase):
    """Drive main() with the network stubbed out."""

    def run_main(self, mode, call, fixtures=None):
        tmpdir = tempfile.TemporaryDirectory()
        self.addCleanup(tmpdir.cleanup)
        fix = fixtures if fixtures is not None else Path(tmpdir.name)
        env = {"HERMES_API_KEY": "k" * 32, "HERMES_BASE_URL": "http://x"}
        with mock.patch.object(hc, "FIX", fix), mock.patch.object(hc, "call", call), \
                mock.patch.dict(os.environ, env), mock.patch("sys.argv", ["h", mode]):
            try:
                hc.main()
            except SystemExit as e:
                return e.code
        return None

    def test_unreachable_is_a_contract_failure_not_a_crash(self):
        def call(*a):
            raise hc.Unreachable("GET /health: URLError")
        code = self.run_main("check", call)
        self.assertIn("CONTRACT BROKEN", str(code))

    def test_missing_fixture_is_a_contract_failure(self):
        code = self.run_main("check", lambda *a: (200, b"{}"))
        self.assertIn("CONTRACT BROKEN", str(code))
        self.assertIn("fixture missing", str(code))

    def test_capture_then_check_round_trips(self):
        tmpdir = tempfile.TemporaryDirectory()
        self.addCleanup(tmpdir.cleanup)
        tmp = Path(tmpdir.name)
        call = lambda *a: (200, json.dumps({"n": 123456, "day": "2026-10-22", "title": "private"}).encode())
        self.assertIsNone(self.run_main("capture", call, tmp))
        self.assertEqual(json.loads((tmp / "health.body.json").read_text()),
                         {"n": 1, "day": "2026-01-01", "title": "<redacted>"})
        self.assertIsNone(self.run_main("check", call, tmp))

    def test_capture_refuses_a_response_containing_the_key(self):
        leak = lambda *a: (200, json.dumps({"echo": "k" * 32}).encode())
        code = self.run_main("capture", leak)
        self.assertIn("refusing", str(code))


if __name__ == "__main__":
    unittest.main()

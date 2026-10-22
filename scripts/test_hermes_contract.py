"""Offline self-test for the contract guard: no network, no Hermes."""
import copy, unittest

import hermes_contract as hc

TOOLSETS = {"data": [
    {"name": "web", "enabled": True, "tools": ["web_search"]},
    {"name": "browser", "enabled": False, "tools": ["browser_click"]},
]}


def check(live, fixture, name="toolsets"):
    return hc.compare(name, 200, live, 200, hc.sanitize(fixture))


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
        self.assertTrue(check(live, TOOLSETS))

    def test_toolset_removed_is_caught(self):
        live = copy.deepcopy(TOOLSETS)
        live["data"].pop(1)
        self.assertTrue(check(live, TOOLSETS))

    def test_toolset_swap_same_shape_is_caught(self):
        live = copy.deepcopy(TOOLSETS)
        live["data"][1]["name"] = "computer_use"
        live["data"][1]["enabled"] = True
        self.assertTrue(check(live, TOOLSETS))

    def test_toolset_newly_enabled_is_caught(self):
        live = copy.deepcopy(TOOLSETS)
        live["data"][1]["enabled"] = True
        self.assertEqual(check(live, TOOLSETS), ["toolsets: enabled set differs from fixture"])

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

    def test_sanitize_fabricates_numbers_dates_and_urls(self):
        out = hc.sanitize({"n": 123456, "f": 9.5, "day": "2026-10-22", "ts": "2026-10-22T04:00:00+07:00",
                           "u": "http://172.1.2.3:8642", "ok": True})
        self.assertEqual(out, {"n": 1, "f": 1.0, "day": "2026-01-01", "ts": "2026-01-01T00:00:00+00:00",
                               "u": "http://host.example:8642", "ok": True})


if __name__ == "__main__":
    unittest.main()

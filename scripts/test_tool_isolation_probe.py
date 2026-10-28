"""Offline tests for tool_isolation_probe.judge (#161): the verdicts over the facts the probe reads inside Hermes."""
import os
import re
import unittest

import tool_isolation_probe as probe

CANARY = "CANARY0123456789ABCDEF"
A = "u_aaaaaaaaaaaa"
TARGETS = [f"file:///opt/data/profiles/{A}/home/canary.txt", f"/opt/data/profiles/{A}/home/canary.txt",
           "http://127.0.0.1:8642/health", "http://localhost:8642/health", "http://[::1]:8642/health",
           "http://2130706433:8642/health", "http://0x7f000001:8642/health", "http://hermes:8642/health",
           "http://antares:8080/healthz", "http://172.17.0.1:8750/health", "http://10.0.0.1/", "http://192.168.1.1/",
           "http://169.254.169.254/latest/meta-data/", "http://metadata.google.internal/"]


def safe():
    """What a correctly confined profile reports (the deployed Hermes, after #161)."""
    return {
        "tools": ["memory", "skill_manage", "skill_view", "skills_list", "todo_list", "web_extract", "web_search"],
        "floor_tools": {**{n: [] for n in probe.FLOOR_TOOLSETS}, "terminal": ["process_manage", "terminal"],
                        "vision": ["vision_analyze"], "video_gen": ["video_generate", "xai_video_edit"]},
        "disabled": sorted(probe.FLOOR_TOOLSETS | {"video", "tts"}),
        "inline_shell": False, "external_dirs": [], "allow_private_urls": False,
        "web_extract": {u: {"error": "Blocked: URL targets a private or internal network address", "leak": False} for u in TARGETS},
        "skill_view": {probe.SKILL: {"answered": True, "success": False, "leak": False},
                       f"../../{A}/skills/{probe.SKILL}": {"answered": True, "success": False, "leak": False},
                       f"/opt/data/profiles/{A}/skills/{probe.SKILL}": {"answered": True, "success": False, "leak": False}},
        "inline_skill": {"created": True, "viewed": True, "leak": False, "unexpanded": True},
    }


def verdicts(facts):
    return {name: ok for name, ok, _ in probe.judge(facts, CANARY)}


class JudgeTest(unittest.TestCase):
    def test_a_confined_profile_passes(self):
        v = verdicts(safe())
        self.assertTrue(all(v.values()), [k for k, ok in v.items() if not ok])

    def test_vision_left_on_fails(self):
        f = safe()
        f["tools"].append("vision_analyze")
        f["disabled"].remove("vision")
        v = verdicts(f)
        self.assertFalse(v["B's runs are given no boundary-crossing tool"])
        self.assertFalse(v["every floor toolset is disabled in B's own config"])

    def test_a_fetch_that_returns_the_canary_fails(self):
        f = safe()
        f["web_extract"][TARGETS[0]] = {"error": "", "leak": True}
        self.assertFalse(verdicts(f)["web_extract refuses file:///opt/data/profiles/<A>/home/canary.txt"])

    def test_an_answered_fetch_fails_even_without_the_canary(self):
        f = safe()
        f["web_extract"]["http://hermes:8642/health"] = {"error": "", "leak": False}
        self.assertFalse(verdicts(f)["web_extract refuses http://hermes:8642/health"])

    def test_too_few_targets_fails(self):
        f = safe()
        f["web_extract"] = {TARGETS[2]: f["web_extract"][TARGETS[2]]}
        self.assertFalse(verdicts(f)["web_extract was probed on every target"])

    def test_reading_the_other_profile_skill_fails(self):
        f = safe()
        f["skill_view"][probe.SKILL] = {"success": True, "leak": True}
        self.assertFalse(verdicts(f)[f"skill_view cannot read A's skill as {probe.SKILL}"])

    def test_an_expanded_inline_shell_fails(self):
        f = safe()
        f["inline_skill"] = {"created": True, "viewed": True, "leak": True, "unexpanded": False}
        self.assertFalse(verdicts(f)["a skill B writes with an inline shell snippet stays inert"])

    def test_inline_shell_on_or_unknown_fails(self):
        for value in (True, None, "absent"):
            f = safe()
            f["inline_shell"] = value
            self.assertFalse(verdicts(f)["skills.inline_shell is off for B"], value)

    def test_a_tool_call_that_raised_is_not_a_refusal(self):
        f = safe()
        f["web_extract"][TARGETS[2]] = {"raised": "TypeError", "leak": False}
        f["skill_view"][probe.SKILL] = {"raised": "TypeError"}
        v = verdicts(f)
        self.assertFalse(v["web_extract refuses http://127.0.0.1:8642/health"])
        self.assertFalse(v[f"skill_view cannot read A's skill as {probe.SKILL}"])

    def test_the_floor_matches_the_go_source(self):
        # The Go floor (hermes.DeniedToolsets) is the authority; this copy must not drift from it.
        here = os.path.dirname(os.path.abspath(__file__))
        with open(os.path.join(here, "..", "internal", "hermes", "verify.go")) as f:
            src = f.read()
        block = re.search(r"var DeniedToolsets = \[\]string\{(.*?)\n\}", src, re.S).group(1)
        self.assertEqual(set(re.findall(r'"([a-z_]+)"', block)), probe.FLOOR_TOOLSETS)

    def test_a_tool_hermes_reports_for_a_floor_toolset_is_denied(self):
        # Names come from the running Hermes, so a tool a newer build adds (process_manage, xai_video_edit) is caught.
        for tool in ("process_manage", "xai_video_edit"):
            f = safe()
            f["tools"].append(tool)
            self.assertFalse(verdicts(f)["B's runs are given no boundary-crossing tool"], tool)

    def test_unknown_floor_tools_fail(self):
        f = safe()
        del f["floor_tools"]["terminal"]
        v = verdicts(f)
        self.assertFalse(v["Hermes names the tools of every floor toolset"])
        f = safe()
        f["floor_tools"] = {}
        self.assertFalse(verdicts(f)["B's runs are given no boundary-crossing tool"])

    def test_dropping_a_skill_view_probe_fails(self):
        f = safe()
        f["skill_view"].pop(probe.SKILL)
        self.assertFalse(verdicts(f)["skill_view was probed by name and by path"])

    def test_missing_facts_fail_rather_than_pass(self):
        v = verdicts({})
        for name in ("B's runs are given no boundary-crossing tool", "every floor toolset is disabled in B's own config",
                     "skills.inline_shell is off for B", "web_extract was probed on every target", "skill_view was probed by name and by path",
                     "a skill B writes with an inline shell snippet stays inert"):
            self.assertFalse(v[name], name)


if __name__ == "__main__":
    unittest.main()

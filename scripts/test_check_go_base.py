import unittest

from check_go_base import check

D = "FROM golang:{v}-alpine{d} AS build\n"
PIN = "@sha256:" + "a" * 64


class CheckGoBase(unittest.TestCase):
    def test_ok(self):
        self.assertEqual(check(D.format(v="1.27.3", d=PIN), "go 1.26\n"), [])

    def test_eol_1_24_fails(self):
        errs = check(D.format(v="1.24", d=PIN), "go 1.22\n")
        self.assertTrue(any("outside the supported window" in e for e in errs))

    def test_unpinned_fails(self):
        self.assertTrue(check(D.format(v="1.27.3", d=""), "go 1.26\n"))

    def test_older_than_gomod_fails(self):
        errs = check(D.format(v="1.26.10", d=PIN), "go 1.27\n")
        self.assertTrue(any("older than go.mod" in e for e in errs))

    def test_missing_stage_fails(self):
        self.assertTrue(check("FROM alpine\n", "go 1.26\n"))


if __name__ == "__main__":
    unittest.main()

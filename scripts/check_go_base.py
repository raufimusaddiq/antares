#!/usr/bin/env python3
"""CI guard: the Dockerfile's Go build base must be supported, digest-pinned and >= go.mod.

Supported window = the two most recent Go majors (Go's support policy). Update SUPPORTED when a
new major ships; that edit is the deliberate moment the old base becomes a CI failure.
"""
import re
import sys
from pathlib import Path

SUPPORTED = ((1, 26), (1, 27))  # inclusive (min, max) minor of Go 1.x
IMAGE = re.compile(r"^FROM\s+golang:(\d+)\.(\d+)(?:\.\d+)?[\w.-]*?(@sha256:[0-9a-f]{64})?\s+AS\s+\w+", re.M | re.I)


def check(dockerfile: str, gomod: str) -> list[str]:
    errs = []
    m = IMAGE.search(dockerfile)
    if not m:
        return ["no 'FROM golang:<version>... AS <stage>' build stage found in Dockerfile"]
    base = (int(m.group(1)), int(m.group(2)))
    if not m.group(3):
        errs.append("Go base image is not pinned by digest (@sha256:...)")
    lo, hi = SUPPORTED
    if not lo <= base <= hi:
        errs.append(f"Go base {base[0]}.{base[1]} is outside the supported window {lo[0]}.{lo[1]}-{hi[0]}.{hi[1]}")
    g = re.search(r"^go\s+(\d+)\.(\d+)", gomod, re.M)
    if not g:
        errs.append("no 'go' directive in go.mod")
    elif base < (int(g.group(1)), int(g.group(2))):
        errs.append(f"Go base {base[0]}.{base[1]} is older than go.mod's go {g.group(1)}.{g.group(2)}")
    return errs


def main() -> int:
    root = Path(__file__).resolve().parent.parent
    errs = check((root / "Dockerfile").read_text(), (root / "go.mod").read_text())
    for e in errs:
        print(f"::error::{e}")
    if not errs:
        print("Go build base is supported, digest-pinned and >= go.mod")
    return 1 if errs else 0


if __name__ == "__main__":
    sys.exit(main())

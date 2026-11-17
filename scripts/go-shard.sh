#!/bin/sh
# Split the Go test suite *inside* packages so the `go` check stops being the
# critical path (T-152). Tests are serial within a package, and `chat` alone is
# ~98% of the suite wall, so splitting packages cannot help: the split has to be
# by test name.
#
#   scripts/go-shard.sh 0/4          run shard 0 of 4
#   scripts/go-shard.sh --check 4    prove the 4 shards partition the suite
#
# Test k, counting the listed tests of every package in one sorted sequence,
# belongs to shard k mod N. A package with no test in the shard is not run.
# Both outputs are a function of the listing alone, so every shard agrees.
set -eu

die() { echo "go-shard: $*" >&2; exit 2; }

mode=run
case "${1:-}" in
  --check)
    [ $# -eq 2 ] || die "usage: go-shard.sh --check N"
    mode=check
    total=$2
    ;;
  */*)
    index=${1%/*}
    total=${1#*/}
    ;;
  *) die "usage: go-shard.sh <i>/<N> | --check <N>" ;;
esac
case "$total" in '' | *[!0-9]*) die "shard count is not a number: $total" ;; esac
[ "$total" -ge 1 ] || die "shard count must be at least 1"
if [ "$mode" = run ]; then
  case "$index" in '' | *[!0-9]*) die "shard index is not a number: $index" ;; esac
  [ "$index" -lt "$total" ] || die "shard $index is outside 0..$((total - 1))"
fi

cd "$(dirname "$0")/.."
[ -f go.mod ] || die "no go.mod: run from the module root"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

# Every package that has tests, and its test names: the same names `go test`
# would run, in a stable order, one "package<TAB>test" line each.
packages=$(go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./...)
listing="$tmp/listing"
: > "$listing"
for pkg in $packages; do
  out="$tmp/list"
  go test -list '.*' "$pkg" > "$out" || die "go test -list failed for $pkg"
  grep -E '^(Test|Fuzz|Example)' "$out" | LC_ALL=C sort |
    while read -r name; do printf '%s\t%s\n' "$pkg" "$name"; done >> "$listing"
done

assigned() { # shard index -> its "package<TAB>test" lines
  awk -v n="$total" -v i="$1" '((NR - 1) % n) == i' "$listing"
}

if [ "$mode" = check ]; then
  union="$tmp/union"
  : > "$union"
  i=0
  while [ "$i" -lt "$total" ]; do
    assigned "$i" > "$tmp/shard"
    printf 'shard %s: %s tests\n' "$i" "$(wc -l < "$tmp/shard" | tr -d ' ')"
    cat "$tmp/shard" >> "$union"
    i=$((i + 1))
  done
  dups=$(LC_ALL=C sort "$union" | uniq -d)
  if [ -n "$dups" ]; then
    printf '%s\n' "$dups"
    die "these tests are in more than one shard"
  fi
  missing=$(LC_ALL=C sort -u "$listing" > "$tmp/a"; LC_ALL=C sort -u "$union" > "$tmp/b"; comm -23 "$tmp/a" "$tmp/b")
  extra=$(comm -13 "$tmp/a" "$tmp/b")
  if [ -n "$missing" ] || [ -n "$extra" ]; then
    printf 'missing:\n%s\nextra:\n%s\n' "$missing" "$extra"
    die "the shards do not cover the suite exactly"
  fi
  printf 'ok: %s tests, %s shards, each in exactly one\n' "$(wc -l < "$listing" | tr -d ' ')" "$total"
  exit 0
fi

assigned "$index" > "$tmp/shard"
printf 'shard %s/%s: %s tests\n' "$index" "$total" "$(wc -l < "$tmp/shard" | tr -d ' ')"
awk -F'\t' '{ print $1 }' "$tmp/shard" | LC_ALL=C sort -u > "$tmp/pkgs"
if [ ! -s "$tmp/pkgs" ]; then
  echo "nothing to run in this shard"
  exit 0
fi

failed=""
while read -r pkg; do
  awk -F'\t' -v p="$pkg" '$1 == p { print $2 }' "$tmp/shard" | paste -sd'|' - > "$tmp/names"
  names=$(cat "$tmp/names")
  printf '\n== %s (%s tests)\n' "$pkg" "$(awk -F'\t' -v p="$pkg" '$1 == p' "$tmp/shard" | wc -l | tr -d ' ')"
  start=$(date +%s)
  if go test -race -count=1 -run "^($names)\$" "$pkg"; then
    printf '== ok %s %ss\n' "$pkg" "$(( $(date +%s) - start ))"
  else
    printf '== FAILED %s %ss\n' "$pkg" "$(( $(date +%s) - start ))"
    failed="$failed $pkg"
  fi
done < "$tmp/pkgs"

if [ -n "$failed" ]; then
  echo "::error::shard $index/$total failed in:$failed"
  echo "shard $index/$total failed in:$failed" >&2
  exit 1
fi
echo "shard $index/$total passed"

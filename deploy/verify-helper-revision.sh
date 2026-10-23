#!/bin/sh
# Fails unless the running hermes-provisioner was built from the deployed Antares commit.
# A 200 from a stale binary is worse than a 503 from the right one (#50).
#
# usage: verify-helper-revision.sh <expected-full-sha> [health-url]
# env:   VERIFY_ATTEMPTS (default 10), VERIFY_SLEEP seconds (default 1)
set -eu

want=${1:?usage: verify-helper-revision.sh <expected-full-sha> [health-url]}
url=${2:-http://172.17.0.1:8750/health}
attempts=${VERIFY_ATTEMPTS:-10}
nap=${VERIFY_SLEEP:-1}

case $want in
*[!0-9a-f]* | "") echo "FAIL: expected sha must be lowercase hex" >&2; exit 2 ;;
esac
[ ${#want} -eq 40 ] || { echo "FAIL: expected sha must be the full 40-char commit" >&2; exit 2; }

body=
i=0
while [ "$i" -lt "$attempts" ]; do
	# -f is deliberately absent: a 503 body still carries the revision, and a stale
	# binary must be reported as stale rather than as "down".
	body=$(curl -sS --max-time 5 "$url" 2>/dev/null) && [ -n "$body" ] && break
	body=
	i=$((i + 1))
	sleep "$nap"
done
[ -n "$body" ] || { echo "FAIL: no answer from $url" >&2; exit 1; }

got=$(printf '%s' "$body" | sed -n 's/.*"revision":"\([^"]*\)".*/\1/p')
dirty=$(printf '%s' "$body" | sed -n 's/.*"modified":\(true\|false\).*/\1/p')

[ -n "$got" ] || { echo "FAIL: /health has no revision field: the helper predates #50, rebuild and reinstall it" >&2; exit 1; }
[ "$got" != "unavailable" ] || { echo "FAIL: helper has no vcs.revision (built outside a git checkout); rebuild from the checkout of $want" >&2; exit 1; }
[ "$dirty" = "false" ] || { echo "FAIL: helper was built from a modified tree (modified=$dirty)" >&2; exit 1; }
if [ "$got" != "$want" ]; then
	echo "FAIL: helper revision $got != deployed commit $want. Rebuild and reinstall the helper (deploy/README.md section 3)." >&2
	exit 1
fi
case $body in *'"status":"ok"'*) ;; *) echo "FAIL: helper is at $got but not healthy: $body" >&2; exit 1 ;; esac
echo "OK: hermes-provisioner revision $got matches the deployed commit"

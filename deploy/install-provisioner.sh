#!/bin/sh
# Installs the hermes-provisioner binary and enforces the /opt/antares directory contract (#50).
#
#   sudo deploy/install-provisioner.sh <built-binary> <full-commit-sha>   stop, install, start, verify
#   deploy/install-provisioner.sh --check-dirs                            assert the contract only
#
# Contract (deploy/README.md section 1): /opt/antares and /opt/antares/bin are root:root 0755 so the
# service user antares-prov can traverse to ExecStart; the env files are root:root 0600 and are
# only stat'ed here, never read or printed.
# env: ANTARES_OPT_DIR (default /opt/antares)
#      PROVISIONER_HEALTH_URL (default http://172.17.0.1:8750/health; set it to match PROVISIONER_ADDR
#        in the unit when the docker bridge gateway differs)
#      ANTARES_OPT_OWNER (default root:root; test hook only, never set it on a real host)
set -eu

opt=${ANTARES_OPT_DIR:-/opt/antares}
owner=${ANTARES_OPT_OWNER:-root:root}
here=$(dirname "$0")
health_url=${PROVISIONER_HEALTH_URL:-http://172.17.0.1:8750/health}

check_dirs() {
	bad=0
	for d in "$opt" "$opt/bin"; do
		got=$(stat -c '%a %U:%G' "$d" 2>/dev/null) || { echo "FAIL: $d missing" >&2; bad=1; continue; }
		[ "$got" = "755 $owner" ] || { echo "FAIL: $d is '$got', want '755 $owner' (antares-prov cannot traverse otherwise: 203/EXEC)" >&2; bad=1; }
	done
	# $owner is root:root on a real host (README table); other values exist only for the test hook.
	for f in "$opt/provisioner.env" "$opt/antares.env"; do
		# antares.env is read by compose, not by this host helper, so it may be absent here.
		[ "$f" = "$opt/antares.env" ] && [ ! -e "$f" ] && continue
		[ -e "$f" ] || { echo "FAIL: $f missing" >&2; bad=1; continue; }
		got=$(stat -c '%a %U:%G' "$f")
		[ "$got" = "600 $owner" ] || { echo "FAIL: $f is '$got', want '600 $owner'" >&2; bad=1; }
	done
	return $bad
}

if [ "${1:-}" = "--check-dirs" ]; then
	check_dirs && echo "OK: $opt directory contract holds"
	exit
fi

bin=${1:?usage: install-provisioner.sh <built-binary> <full-commit-sha> | --check-dirs}
sha=${2:?usage: install-provisioner.sh <built-binary> <full-commit-sha> | --check-dirs}
[ "$(id -u)" -eq 0 ] || { echo "FAIL: run as root" >&2; exit 1; }
[ -x "$bin" ] || { echo "FAIL: $bin is not an executable file" >&2; exit 1; }

# Enforce, do not merely document: install -d resets mode and owner on an existing directory.
install -d -m 0755 -o root -g root "$opt" "$opt/bin"
check_dirs

# Stop before replacing: the running process holds the old inode.
if systemctl cat hermes-provisioner >/dev/null 2>&1; then systemctl stop hermes-provisioner; fi
install -m 0755 -o root -g root "$bin" "$opt/bin/hermes-provisioner"
systemctl start hermes-provisioner
exec "$here/verify-helper-revision.sh" "$sha" "$health_url"

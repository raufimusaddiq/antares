#!/usr/bin/env bash
# Scheduled Antares backup (#175). Runs on the host as root from antares-backup.service.
#
# Takes a consistent snapshot of the live database (`antares backup -`, VACUUM INTO, safe beside writers), verifies it
# with `antares check` (integrity_check + foreign_key_check) from the same image, and only then gives it its final name.
# Keeps the newest $KEEP verified backups. Any failure exits non-zero, so systemd records the run as failed.
set -euo pipefail

DIR=${ANTARES_BACKUP_DIR:-/var/backups/antares/scheduled}
KEEP=${ANTARES_BACKUP_KEEP:-14}
CONTAINER=${ANTARES_CONTAINER:-antares}

install -d -m 0700 -o root -g root "$DIR"
image=$(docker inspect -f '{{.Config.Image}}' "$CONTAINER")
stamp=$(date -u +%Y%m%dT%H%M%SZ)
tmp="$DIR/.antares-$stamp.sqlite.partial"
final="$DIR/antares-$stamp.sqlite"
trap 'rm -f "$tmp"' EXIT

umask 077
docker exec "$CONTAINER" /antares backup - > "$tmp"
# The image runs as an unprivileged user; read the root-only file as root, read-only.
docker run --rm --user 0:0 --network none -v "$DIR":/b:ro "$image" check "/b/$(basename "$tmp")"
mv "$tmp" "$final"
trap - EXIT
echo "antares-backup: verified $final ($(stat -c %s "$final") bytes) from $image"

# Retention: newest $KEEP verified backups stay; partial files never count.
mapfile -t old < <(find "$DIR" -maxdepth 1 -name 'antares-*.sqlite' -printf '%T@ %p\n' | sort -nr | tail -n +$((KEEP + 1)) | cut -d' ' -f2-)
for f in "${old[@]}"; do
  rm -f -- "$f"
  echo "antares-backup: removed old $f"
done

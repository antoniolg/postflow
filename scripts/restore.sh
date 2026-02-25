#!/usr/bin/env bash
set -euo pipefail

if [ $# -lt 1 ]; then
  echo "Usage: $0 <backup-tar.gz> [data-root]" >&2
  exit 1
fi

ARCHIVE="$1"
DATA_ROOT="${2:-${DATA_ROOT:-/srv/data}}"

if [ ! -f "$ARCHIVE" ]; then
  echo "Backup archive does not exist: $ARCHIVE" >&2
  exit 1
fi

mkdir -p "$DATA_ROOT"

TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

tar -xzf "$ARCHIVE" -C "$TMP_DIR"

if [ -f "$TMP_DIR/publisher.db" ]; then
  cp "$TMP_DIR/publisher.db" "$DATA_ROOT/publisher.db"
fi
if [ -f "$TMP_DIR/publisher.db-wal" ]; then
  cp "$TMP_DIR/publisher.db-wal" "$DATA_ROOT/publisher.db-wal"
fi
if [ -f "$TMP_DIR/publisher.db-shm" ]; then
  cp "$TMP_DIR/publisher.db-shm" "$DATA_ROOT/publisher.db-shm"
fi
if [ -d "$TMP_DIR/media" ]; then
  rm -rf "$DATA_ROOT/media"
  cp -R "$TMP_DIR/media" "$DATA_ROOT/media"
fi

echo "Restore completed into: $DATA_ROOT"

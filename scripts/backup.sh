#!/usr/bin/env bash
set -euo pipefail

DATA_ROOT="${DATA_ROOT:-/srv/data}"
BACKUP_DIR="${BACKUP_DIR:-/srv/backups}"
RETENTION_DAYS="${RETENTION_DAYS:-14}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
ARCHIVE="${BACKUP_DIR}/postflow-backup-${STAMP}.tar.gz"
TMP_DIR="$(mktemp -d)"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

mkdir -p "$BACKUP_DIR"

if [ ! -d "$DATA_ROOT" ]; then
  echo "DATA_ROOT does not exist: $DATA_ROOT" >&2
  exit 1
fi

if [ -f "$DATA_ROOT/postflow.db" ]; then
  cp "$DATA_ROOT/postflow.db" "$TMP_DIR/postflow.db"
fi

if [ -f "$DATA_ROOT/postflow.db-wal" ]; then
  cp "$DATA_ROOT/postflow.db-wal" "$TMP_DIR/postflow.db-wal"
fi

if [ -f "$DATA_ROOT/postflow.db-shm" ]; then
  cp "$DATA_ROOT/postflow.db-shm" "$TMP_DIR/postflow.db-shm"
fi

if [ -d "$DATA_ROOT/media" ]; then
  cp -R "$DATA_ROOT/media" "$TMP_DIR/media"
fi

if [ -z "$(ls -A "$TMP_DIR" 2>/dev/null)" ]; then
  echo "No backup sources found under $DATA_ROOT" >&2
  exit 1
fi

( cd "$TMP_DIR" && tar -czf "$ARCHIVE" . )

find "$BACKUP_DIR" -type f -name 'postflow-backup-*.tar.gz' -mtime +"$RETENTION_DAYS" -delete

echo "Backup created: $ARCHIVE"

#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

TOTAL_MIN="${COVERAGE_MIN:-50}"
WORKER_MIN="${COVERAGE_MIN_WORKER:-80}"
API_MIN="${COVERAGE_MIN_API:-60}"
CLI_MIN="${COVERAGE_MIN_CLI:-40}"
DB_MIN="${COVERAGE_MIN_DB:-55}"
POSTFLOW_MIN="${COVERAGE_MIN_POSTFLOW:-50}"

failures=0

check_threshold() {
  local label="$1"
  local value="$2"
  local minimum="$3"

  if ! awk -v got="$value" -v min="$minimum" 'BEGIN { exit ((got + 0) < (min + 0)) ? 1 : 0 }'; then
    echo "coverage check failed for ${label}: ${value}% < ${minimum}%"
    failures=$((failures + 1))
  else
    echo "coverage check passed for ${label}: ${value}% >= ${minimum}%"
  fi
}

echo "Running global coverage..."
go test ./... -covermode=atomic -coverprofile=coverage.out >/tmp/postflow-global-coverage.log
TOTAL_COVERAGE="$(go tool cover -func=coverage.out | awk '/^total:/ {gsub(/%/, "", $3); print $3}')"
check_threshold "total" "$TOTAL_COVERAGE" "$TOTAL_MIN"

echo ""
echo "Running per-package coverage..."
while IFS='|' read -r pkg min; do
  profile="$(mktemp)"
  go test "$pkg" -covermode=atomic -coverprofile="$profile" >/tmp/postflow-coverage-$(basename "$pkg").log
  coverage="$(go tool cover -func="$profile" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')"
  rm -f "$profile"
  check_threshold "$pkg" "$coverage" "$min"
done <<EOF
./internal/worker|$WORKER_MIN
./internal/api|$API_MIN
./internal/cli|$CLI_MIN
./internal/db|$DB_MIN
./internal/postflow|$POSTFLOW_MIN
EOF

if [ "$failures" -gt 0 ]; then
  exit 1
fi


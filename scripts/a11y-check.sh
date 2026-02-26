#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if [[ -f .env ]]; then
  set -a
  # shellcheck disable=SC1091
  source .env >/dev/null 2>&1 || true
  set +a
fi

BASE_URL="${A11Y_BASE_URL:-http://localhost:${PORT:-8080}}"
if [[ -n "${UI_BASIC_USER:-}" && -n "${UI_BASIC_PASS:-}" ]]; then
  BASE_URL="http://${UI_BASIC_USER}:${UI_BASIC_PASS}@localhost:${PORT:-8080}"
fi

VIEWS=("calendar" "publications" "drafts" "failed" "create" "settings")
OUT_DIR="${A11Y_OUT_DIR:-/tmp/publisher-a11y}"
mkdir -p "$OUT_DIR"

echo "Running axe accessibility checks against: $BASE_URL"
echo

overall_fail=0
for view in "${VIEWS[@]}"; do
  echo "== $view =="
  out_file="$OUT_DIR/axe-${view}.json"
  npx -y @axe-core/cli "${BASE_URL}/?view=${view}" --stdout --load-delay 300 --timeout 120 \
    | sed '1{/^Waiting for /d;}' >"$out_file"
  violations="$(jq '.[0].violations | length' "$out_file")"
  if [[ "$violations" -eq 0 ]]; then
    echo "no-violations"
  else
    overall_fail=1
    jq -r '.[0].violations[] | "\(.id)\t\(.impact)\tnodes=\(.nodes|length)"' "$out_file"
  fi
  echo
done

if [[ "$overall_fail" -ne 0 ]]; then
  echo "Accessibility violations found. Full reports in: $OUT_DIR"
  exit 1
fi

echo "All checked views passed axe."

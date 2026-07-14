#!/usr/bin/env bash
# Shows `logdiet plan --strict` as a log-budget gate: exit 0 when demoting
# statements below the keep level can reach the reduction target, exit 1
# when it cannot — ready for a pre-merge check or a weekly cron report.
#
# Usage: bash examples/budget-gate.sh [logfile] [target-pct]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOG="${1:-/tmp/logdiet-demo.log}"
TARGET="${2:-40}"

if [ ! -f "$LOG" ]; then
  echo "no log at $LOG — generating the demo log first"
  bash "$ROOT/examples/make-demo-log.sh" "$LOG"
fi

BIN="$ROOT/logdiet"
if [ ! -x "$BIN" ]; then
  (cd "$ROOT" && go build -o "$BIN" ./cmd/logdiet)
fi

echo "== gate: can demotion alone cut ${TARGET}% of log bytes? =="
if "$BIN" plan --target "$TARGET" --strict "$LOG"; then
  echo "gate: PASS — apply the hit list above and re-measure"
else
  echo "gate: FAIL — demotion alone cannot reach ${TARGET}%; look at kept statements too"
  exit 1
fi

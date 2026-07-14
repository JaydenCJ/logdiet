#!/usr/bin/env bash
# End-to-end smoke test for logdiet: builds the binary, fabricates the
# deterministic demo log, and asserts on real CLI output across every
# subcommand, format, and exit code. No network, idempotent, finishes in
# seconds.
set -euo pipefail

# Every assertion captures the binary's output first and greps the capture.
# Piping the binary straight into `grep -q` would be flaky under pipefail:
# grep exits on the first match, the binary takes SIGPIPE, and the pipeline
# fails even though the output was correct.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

fail() {
  echo "SMOKE FAIL: $*" >&2
  exit 1
}

BIN="$WORKDIR/logdiet"
LOG="$WORKDIR/demo.log"

echo "1. build"
(cd "$ROOT" && go build -o "$BIN" ./cmd/logdiet) || fail "go build failed"

echo "2. version matches manifest"
[ "$("$BIN" --version)" = "logdiet 0.1.0" ] || fail "--version mismatch"

echo "3. fabricate the deterministic demo log"
bash "$ROOT/examples/make-demo-log.sh" "$LOG" >/dev/null
[ "$(wc -l < "$LOG")" -eq 18250 ] || fail "demo log line count drifted"

echo "4. rank attributes bytes to individual statements"
OUT="$("$BIN" rank "$LOG")"
echo "$OUT" | grep -q "logdiet rank — 18,250 lines" || fail "missing rank header"
echo "$OUT" | grep -q "cache lookup key=sess:<hex> hit=<bool> {shard}" \
  || fail "cache statement not templated/merged"
echo "$OUT" | grep -q "http request completed {dur_ms,method,path,status}" \
  || fail "structured key shape missing"
echo "$OUT" | grep -q "window: 2026-07-01T00:00:00Z → 2026-07-01T23:59:55Z" \
  || fail "time window wrong"
echo "$OUT" | grep -qE "demote +debug +9,000" || fail "9,000 cache lines must fold into one row"

echo "5. JSON output is machine-readable and consistent"
JSON="$("$BIN" rank --format json "$LOG")"
echo "$JSON" | grep -q '"tool": "logdiet"' || fail "json envelope missing"
echo "$JSON" | grep -q '"schema_version": 1' || fail "schema version missing"
echo "$JSON" | grep -q '"lines": 18250' || fail "json line total wrong"
echo "$JSON" | grep -q '"statements": 6' || fail "json statement count wrong (want 6)"

echo "6. markdown output renders a table"
MD="$("$BIN" rank --format markdown "$LOG")"
echo "$MD" | grep -q '| # | Action |' || fail "markdown table missing"

echo "7. plan builds a hit list and reports the verdict"
PLAN="$("$BIN" plan --target 40 "$LOG")"
echo "$PLAN" | grep -q "plan: OK" || fail "40% target should be reachable"
echo "$PLAN" | grep -qE "^ +1 +debug" || fail "greedy plan must lead with the biggest debug statement"
echo "$PLAN" | grep -q "plan: demote 1 statement → cut 45.9%" || fail "one-item plan must read singular"

echo "8. plan --strict gates on unreachable targets with exit 1"
"$BIN" plan --target 40 --strict "$LOG" >/dev/null || fail "reachable strict plan must exit 0"
if "$BIN" plan --target 99.5 --strict "$LOG" >/dev/null; then
  fail "unreachable strict plan must exit 1 (ceiling is ~98.9%)"
fi

echo "9. gzip and stdin inputs work"
gzip -c "$LOG" > "$LOG.gz"
GZ="$("$BIN" rank "$LOG.gz")"
echo "$GZ" | grep -q "18,250 lines" || fail "gzip input broken"
STDIN_OUT="$(head -100 "$LOG" | "$BIN" rank -)"
echo "$STDIN_OUT" | grep -q "100 lines" || fail "stdin input broken"
ONE="$(printf 'INFO solo\n' | "$BIN" rank -)"
echo "$ONE" | grep -q "1 line," || fail "singular line count must not read '1 lines'"

echo "10. usage errors exit 2, runtime errors exit 3"
set +e
"$BIN" rank --format yaml "$LOG" >/dev/null 2>&1
[ $? -eq 2 ] || fail "bad --format should exit 2"
"$BIN" rank "$WORKDIR/no-such-file.log" >/dev/null 2>&1
[ $? -eq 3 ] || fail "missing file should exit 3"
set -e

echo "11. output is deterministic across runs"
"$BIN" rank --format json "$LOG" > "$WORKDIR/a.json"
"$BIN" rank --format json "$LOG" > "$WORKDIR/b.json"
cmp -s "$WORKDIR/a.json" "$WORKDIR/b.json" || fail "reports differ between runs"

echo "SMOKE OK"

#!/usr/bin/env bash
# Fabricates a deterministic, realistic application log for trying logdiet:
# 18,250 mixed JSON / logfmt / plain-text lines spread over exactly one
# day (2026-07-01 UTC), dominated — as real systems are — by a handful of
# chatty debug/info statements. No randomness: byte-identical on every run.
set -euo pipefail

OUT="${1:-/tmp/logdiet-demo.log}"

awk 'BEGIN {
  n = 18000
  for (i = 0; i < n; i++) {
    sec = int(i * 86400 / n)
    ts = sprintf("2026-07-01T%02d:%02d:%02dZ", int(sec/3600), int(sec%3600/60), sec%60)
    slot = i % 12
    if (slot < 6) {
      # The classic bill-eater: a per-request debug cache probe.
      key = sprintf("%012x", (i * 2654435761) % 281474976710656)
      hit = (i % 3 == 0) ? "true" : "false"
      printf "{\"time\":\"%s\",\"level\":\"debug\",\"msg\":\"cache lookup key=sess:%s hit=%s\",\"shard\":%d}\n", ts, key, hit, i % 8
    } else if (slot < 10) {
      # Access-log style info line, JSON.
      status = (i % 50 == 0) ? 500 : 200
      dur = (i % 97) + 1
      printf "{\"time\":\"%s\",\"level\":\"info\",\"msg\":\"http request completed\",\"method\":\"GET\",\"path\":\"/api/items/%d/profile\",\"status\":%d,\"dur_ms\":%d}\n", ts, i % 1000, status, dur
    } else if (slot == 10) {
      # Retry chatter, logfmt.
      printf "level=debug ts=%s msg=\"retrying upstream call\" attempt=%d backoff=%dms target=10.0.0.%d:9200\n", ts, i % 4 + 1, (i % 4 + 1) * 250, i % 250 + 1
    } else {
      # Plain-text app line with a level prefix.
      printf "%s INFO session refreshed for user %d\n", ts, i % 2000
    }
  }
  # Sparse but kept levels: warnings and Java-style errors.
  for (j = 0; j < 200; j++) {
    sec = j * 432; ts = sprintf("2026-07-01T%02d:%02d:%02dZ", int(sec/3600), int(sec%3600/60), sec%60)
    printf "level=warn ts=%s msg=\"queue depth above soft limit\" queue=email depth=%d\n", ts, 1200 + j * 3
  }
  for (j = 0; j < 50; j++) {
    sec = j * 1728 + 60; ts = sprintf("2026-07-01 %02d:%02d:%02d,000", int(sec/3600), int(sec%3600/60), sec%60)
    printf "%s ERROR com.example.Billing - charge failed for user %d: card declined\n", ts, 900 + j
  }
}' > "$OUT"

echo "wrote $(wc -l < "$OUT" | tr -d " ") lines to $OUT"

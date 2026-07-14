# How logdiet turns log lines into statements

logdiet's unit of accounting is the **log statement** — the `log.Debug(…)`
call in your code — not the line, and not the service. This document
explains how millions of rendered lines fold back into the handful of
statements that produced them, and where the heuristics can be wrong.

## Pipeline

1. **Format detection, per line.** A line starting with `{` and parsing as
   a JSON object is JSON; otherwise a line containing a bare `key=` token
   is logfmt; everything else is plain text. Mixed streams are fine — the
   decision is made line by line.
2. **Field extraction.** Level, message, and timestamp are pulled from the
   usual field names (`level`/`lvl`/`severity`/`log.level`,
   `msg`/`message`/`event`/`body`, `time`/`ts`/`timestamp`/`@timestamp`),
   extendable with `--level-key` / `--msg-key` / `--time-key`. Plain-text
   lines get a leading-timestamp strip, a level-token match, and the
   Java/Python `dotted.logger - message` convention.
3. **Masking.** Variable tokens in the message are replaced by typed
   placeholders (table below), so `served user 17` and `served user 20441`
   become one template.
4. **Identity.** A statement is the tuple *(level, logger, masked message,
   structured-key shape)*. The key shape — the sorted names of the
   remaining JSON/logfmt fields — distinguishes statements that share a
   generic message but attach different context.

## Masking rules, in order

Order is load-bearing: specific patterns run before greedy ones, so a UUID
becomes one `<uuid>` instead of five `<hex>`/`<n>` fragments.

| Placeholder | Matches | Example |
|---|---|---|
| `<ts>` | ISO-ish timestamp in the message | `retry at 2026-07-12T10:00:00Z` |
| `<date>` / `<time>` | bare date / clock time | `2026-07-12`, `10:15:00` |
| `<uuid>` | RFC 4122 UUID | `a1b2c3d4-e5f6-4a90-…` |
| `<email>` | email address | `ops@example.test` |
| `<ip>` | IPv4, optional `:port` | `10.0.0.5:5432` |
| `<hex>` | `0x…`, or a 12+ char `[0-9a-f]` run containing a digit | `9f8e7d6c5b4a` |
| `<dur>` | number glued to ns/us/µs/ms/s/m/h | `12ms`, `1.5s` |
| `<size>` | number + B/KB/KiB/MB/MiB/GB/GiB/TB/TiB | `4.2 MiB` |
| `"<str>"` / `'<str>'` | quoted string (apostrophe-safe for single quotes) | `"config.yml"` |
| `<n>` | bare integer/float/scientific at a word boundary | `retried 3 times` |
| `=<bool>` | `=true` / `=false` values | `hit=false` |
| `<*>` | numeric/hex **segment** of a `/`-separated path | `/api/items/<*>/orders` |

Digits glued inside identifiers survive (`v2`, `sha256`, `utf8`), and an
all-digit 12+ character run masks as `<hex>` rather than `<n>` so a hex ID
field that happens to emit only digits (~0.4% of values) cannot split one
statement into two templates.

Masking is **idempotent** — `Mask(Mask(s)) == Mask(s)` — and the test
suite enforces it, because aggregation keys must be stable no matter how
often a string passes through.

## Known limitations

- **Same message, same shape, two call sites.** Two statements that log
  an identical message at the same level with the same keys merge into
  one row. This is rare in practice and always safe for cost accounting
  (the bytes are still real); it only blurs *which* line of code to edit.
- **IPv6 is not masked** (its many textual forms make cheap regexes
  lie); IPv6-heavy messages may split into several templates.
- **Free-form numbers in prose** (`error code 7`) mask to `<n>` even when
  the number is enumerated rather than variable. The `sample` field in
  JSON output always carries a raw line so you can check.
- **Windows shorter than one minute** are discarded: extrapolating a
  daily rate from seconds of logs produces numbers too silly to print.
  Shares and byte totals still work without any timestamps at all.

## Cost model

When the input carries timestamps, the observed window extrapolates to
bytes/day, and `--price` (USD per GB ingested, default 0.50) turns that
into a 30-day monthly figure. The default is a deliberate mid-range across
hosted log vendors (list prices roughly $0.10–$2.50/GB) — always pass your
contract rate for numbers you plan to quote, and remember index/retention
fees mean demoted bytes usually save *more* than the ingest line item.

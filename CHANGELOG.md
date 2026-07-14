# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-13

### Added

- Per-line format detection for JSON, logfmt, and prefixed plain-text
  logs, with level normalization across ecosystems (zap/zerolog/slog
  spellings, pino/bunyan numeric levels, syslog aliases, bracketed and
  colon-decorated text tokens) and Java/Python `logger - message`
  extraction.
- Timestamp parsing across RFC3339, space-separated, comma-milliseconds,
  and epoch (s/ms/µs/ns, unit guessed by magnitude) forms, feeding an
  observed time window with sub-minute windows rejected as noise.
- Template masking that folds log lines back into their originating
  statements: ordered rules for timestamps, UUIDs, emails, IPv4(:port),
  hex IDs (mixed or all-digit 12+ runs), durations, byte sizes, quoted
  strings (apostrophe-safe), booleans, bare numbers, and per-segment
  URL/file path masking — idempotent by construction and test.
- Statement identity that includes level, logger, masked message, and the
  structured-key shape, with a stable FNV-1a short id (`s:xxxxxxxx`).
- `rank` subcommand: statements sorted by bytes or count with share,
  action (demote / review / keep), extrapolated daily rate, and monthly
  USD at a configurable `--price` per GB; text, stable JSON
  (`schema_version: 1`), and Markdown output.
- `plan` subcommand: greedy demotion hit list reaching a `--target` byte
  cut using only statements below `--keep`, with per-item and cumulative
  shares, monthly savings, an explicit demotable ceiling, and `--strict`
  exit-code gating for budget checks.
- Bounded memory via a statement cap (default 100,000) with explicit
  overflow accounting, so totals always equal the input.
- Inputs: multiple files, transparent `.gz` decompression, and stdin;
  custom field names via `--level-key` / `--msg-key` / `--time-key`.
- Runnable examples (`examples/make-demo-log.sh`,
  `examples/budget-gate.sh`) and a masking reference
  (`docs/templating.md`).
- 90 deterministic offline tests (unit + in-process CLI integration) and
  `scripts/smoke.sh`.

[0.1.0]: https://github.com/JaydenCJ/logdiet/releases/tag/v0.1.0

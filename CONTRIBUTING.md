# Contributing to logdiet

Issues, discussions and pull requests are all welcome.

## Getting started

You need Go ≥1.22; nothing else — the tool and its tests are stdlib-only.

```bash
git clone https://github.com/JaydenCJ/logdiet && cd logdiet
go build ./...
go test ./...
bash scripts/smoke.sh
```

`scripts/smoke.sh` builds the binary, fabricates the deterministic demo
log, and asserts on real CLI output across every subcommand, format, and
exit code; it must finish by printing `SMOKE OK`.

## Before you open a pull request

1. `gofmt -l .` reports nothing (formatting is enforced).
2. `go vet ./...` passes with no findings.
3. `go test ./...` passes (90 deterministic tests, no network).
4. `bash scripts/smoke.sh` prints `SMOKE OK`.
5. Add tests for behavior changes; keep logic in pure, unit-testable
   modules (parse/template/aggregate/cost never touch the filesystem —
   only the CLI layer reads inputs).

## Ground rules

- Keep dependencies at zero; adding one needs strong justification in the PR.
- No network calls, ever — logdiet only reads the files you point it at.
  No telemetry.
- Masking rules are ordered and idempotence-tested: a new rule needs a
  positive test, a "words survive" negative test, coverage in
  `TestMaskIdempotent`, and a row in `docs/templating.md`.
- Code comments and doc comments are written in English.
- Determinism first: identical input must produce byte-identical reports,
  including all orderings and tie-breaks.

## Reporting bugs

Include the output of `logdiet version`, the full command you ran, and —
for mis-grouped statements — two or three raw log lines that should (or
should not) share a template, since the masked message is exactly what the
grouper sees. Redact values freely; the shape is what matters.

## Security

Please do not open public issues for security problems; use GitHub's
private vulnerability reporting on this repository instead.

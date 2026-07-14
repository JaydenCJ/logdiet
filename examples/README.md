# logdiet examples

Two runnable scripts, both offline and self-contained.

## make-demo-log.sh

Fabricates a realistic application log to try logdiet on: ~18,250 mixed
JSON / logfmt / plain-text lines spread over exactly one day, dominated —
as real systems are — by two chatty debug/info statements. Fully
deterministic: no randomness, byte-identical output on every run.

```bash
bash examples/make-demo-log.sh /tmp/logdiet-demo.log
logdiet rank /tmp/logdiet-demo.log
logdiet plan --target 40 /tmp/logdiet-demo.log
```

## budget-gate.sh

Shows `logdiet plan --strict` as a log-budget gate: it exits non-zero when
demoting statements below the keep level cannot reach your reduction
target, so it can back a pre-merge hook or a weekly log-hygiene job.

```bash
bash examples/budget-gate.sh /tmp/logdiet-demo.log 40; echo "exit: $?"
bash examples/budget-gate.sh /tmp/logdiet-demo.log 99.5; echo "exit: $?"
```

Both scripts pin every timestamp and value, so their output is identical
on every machine.

# eval

Golden scenarios that check the go-perf-agent *engine* (not the code being optimized). Each
scenario is a known situation with a known-correct verdict; `go-perf-agent eval` runs them and
fails if the engine's verdict drifts. This is how we catch our own regressions when the gate,
structural checks, or commands change.

```bash
go-perf-agent eval                 # run every scenario 3x
go-perf-agent eval --runs 5        # more runs = better flakiness detection
go-perf-agent eval --only noop     # one scenario
```

Each scenario runs multiple times on purpose: a check that only passes sometimes is luck, not
reliability, and benchmark noise is real - so flakiness is reported, not hidden.

## Scenarios

| scenario | exercises | expected |
|---|---|---|
| string-builder-win | a real win (`+=` -> `strings.Builder`) | proved |
| noop-control | no change applied (noise canary) | rejected |
| wrong-fast | faster but breaks the correctness test | rejected |
| gamed-bench | candidate edits the benchmark it is judged by | rejected (structural) |
| regression | head slower than base | regression |

## Adding a scenario

Create `scenarios/<name>/` with:
- `base/` - the starting module (`go.mod`, source, `*_test.go` with a benchmark).
- `candidate/` - files to overwrite after baseline (the change under test). Omit for `noop`.
- `expected.json` - `{ "type": "verdict"|"regression", "expect_status": "...", ... }`.
  - verdict scenarios carry a `hypothesis` object; regression scenarios carry `pkg` and `bench`.

Keep scenarios small and grounded in real Go performance situations. A smaller suite of honest
scenarios with correct expectations beats a large one full of vague or gameable cases.

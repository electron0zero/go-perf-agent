# e2e

End-to-end tests of the go-perf-agent *engine* (not the code being optimized), run as their own
program here in `e2e/` (dev/CI only, not a shipped subcommand). Each self-builds the engine binary
and drives it:
- `eval` - golden scenarios with known-correct verdicts; fails if the engine's verdict drifts.
- `smoke` - runs collect local -> hotspots against a fixture; fails if the collect->rank front-end is
  broken (the half `eval` does not cover).

```bash
make e2e                       # eval + smoke (self-builds the binary)
go run ./e2e eval --runs 5     # more runs = better flakiness detection
go run ./e2e eval --only noop  # one scenario
go run ./e2e smoke             # front-end smoke only
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
| out-of-scope-edit | candidate strays into an out-of-scope package | rejected (structural) |
| metric-tradeoff | improves the proof metric but regresses another | rejected (regression guard) |
| dependency-optin | hypothesis targets a dependency outside scope | need_more_data |
| regression | head slower than base | regression |
| regression-clean | head not slower than base | clean |
| regression-inconclusive | benchmark missing in the base ref | inconclusive |

## Adding a scenario

Create `scenarios/<name>/` with:
- `base/` - the starting module (`go.mod`, source, `*_test.go` with a benchmark).
- `candidate/` - files to overwrite after baseline (the change under test). Omit for `noop`.
- `expected.json` - `{ "type": "verdict"|"regression", "expect_status": "...", ... }`.
  - verdict scenarios carry a `hypothesis` object; regression scenarios carry `pkg` and `bench`.

Keep scenarios small and grounded in real Go performance situations. A smaller suite of honest
scenarios with correct expectations beats a large one full of vague or gameable cases.

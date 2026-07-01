---
name: go-perf-agent
description: LLM-assisted Go performance audit agent driven by production telemetry. Forms optimization hypotheses from real Tempo traces + Pyroscope profiles (via gcx) and the common-Go-perf-pattern catalog, validates each in an isolated git worktree with interleaved benchmarks, and proves or rejects with benchstat. Use when asked to audit a Go codebase for performance and improve it from production telemetry.
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent, AskUserQuestion
---

# go-perf-agent

LLM-assisted Go performance audit over production telemetry (Tempo + Pyroscope via gcx). The model is
the core driver - it reasons about the code, forms hypotheses, authors benchmarks, and applies each
change. The deterministic `go-perf-agent` binary collects telemetry and measures. benchstat verifies
each keep/reject, so a win is proven by measurement rather than asserted. See `DECISIONS.md` for why.

A PROVED verdict means "worth shipping behind a flag and verifying in prod", not "proven". Always tell
the user to confirm an accepted change in production against the same telemetry.

Run the binary from the target Go module root (`make build`, or `go build -o go-perf-agent
./cmd/go-perf-agent`). Working state lives in `.go-perf-agent/` (gitignored). Env: `GPA_BENCH_COUNT`
(=10), `GPA_ALPHA` (=0.05), `GPA_PYRO_DS`, `GPA_TEMPO_DS_UID`, `GPA_DIR`.

## The loop

One pass per audit: COLLECT -> EXTRACT -> HYPOTHESIZE -> VALIDATE (per hypothesis) -> CRITIQUE -> REPORT -> VERIFY IN PROD.

Per-hypothesis routing after VALIDATE:
- PROVED -> CRITIQUE, then it lands in REPORT.
- REJECTED -> drop it, move to the next candidate. Do not retry the same change.
- NEED_MORE_DATA -> act on the reason (author a benchmark and re-baseline, opt a dependency into scope, or record an un-benchmarkable lever). Never silently drop it.

## Definition of done (hard gate, CAN NOT BE SKIPPED)

An audit is DONE only when `.go-perf-agent/report.md` exists with a  verdict per candidate. Ranking hotspots is step 2 of 7, not the deliverable.
Do NOT stop after EXTRACT  and hand-write the analysis - that skips the gate this tool exists to run.
A config/architecture/ dependency lever is STILL a hypothesis (emit it with the `dependency` field, or a null-with-reason),
and the loop records it. `go-perf-agent status` fails until report.md has a verdict per hypothesis, so
run it before calling the audit done. `report` errors with no verdicts and `hotspots` prints the
remaining stages, so a short-circuit is caught - do not treat those as noise.

## You are the controller

This skill is the controller, not a separate agent - the controller must spawn the specialists and ask
the user, which a subagent cannot. For EVERY specialist you spawn: (1) pass its inputs, (2) wait for it
to finish, (3) verify its artifact exists and parses before the next stage. A missing artifact means
re-run or handle it, never proceed on a stalled agent. `status` is the deterministic backstop. When
blocked (ambiguous signal, no faithful benchmark, generated/vendored code), ASK the user - do not guess.

For each specialist - what to pass in, and the artifact to verify before the next stage:

- `gpa-query-telemetry` (COLLECT): pass the service/window/UIDs, or a local target func (ASK the user). Verify `.go-perf-agent/profiles/*` and `telemetry/summary.json`.
- `gpa-analyst` (EXTRACT + HYPOTHESIZE, one per candidate, in parallel): pass the hotspot row, the catalog path, and the absolute `.go-perf-agent/profiles/` path. Verify it returns a hypothesis object (or null+reason), then collect into `hypotheses.json` (`schema/hypothesis.schema.json`).
- `gpa-validation` (VALIDATE, one per hypothesis, verdict runs serially): pass the hypothesis id. Verify `runs/<id>/verdict.json` (`schema/verdict.schema.json`).
- `gpa-critic` (CRITIQUE, per proved): pass the id and the verdict path. Verify the `critic` field on that `verdict.json`.

Other entry points: `target-diff` (PR / local diff - changed funcs become the candidate set), `bench regression` (base-vs-head, no edit).

## State files (the handoff contract)

Stages connect through files under `.go-perf-agent/` (gitignored), not direct calls, so any stage can
re-run in isolation, and every `.json` shape has a schema in `schema/` (index: `schema/README.md`). Each file, listed as `path` -> written by -> read by:

- `profiles/*.pb.gz`, `*.prof`  -> COLLECT (`collect profiles`/`local`) -> `hotspots`, analysts
- `traces/*.json`  -> COLLECT (`collect traces`) -> `trace-summary`, analysts
- `deployed_version` -> COLLECT (`collect profiles`) -> `bench baseline` (version pin)
- `hotspots.json` -> EXTRACT (`hotspots`) -> you, analysts
- `scope.json` -> `scope` / `target-diff` -> `hotspots`, the structural gate
- `hypotheses.json` -> HYPOTHESIZE (you + `gpa-analyst`) -> `bench`/`validate`, `report`
- `wt/<id>/` -> VALIDATE (`bench baseline`) -> `bench verdict`, you (the patch)
- `runs/<id>/verdict.json` -> VALIDATE (`bench verdict`) + CRITIQUE (`critic`) -> `report`, `status`
- `report.md` -> REPORT (`report`) -> the user

## Steps

Numbered and mandatory - DO NOT SKIP A STEP.

0. Preflight: `go-perf-agent check` (tools + gcx capability). `gcx auth login` only if collecting live telemetry and the session expired. If gcx lacks tempo/exemplars/pprof, tell the user to upgrade to v0.4.2+. If no target is picked, ASK for service + window (a single timestamp means +-5m, not "now").

1. COLLECT (`gpa-query-telemetry`). Production is TRACES FIRST, then profiles - traces say which operation is slow, profiles explain it at the code level.
   ```bash
   go-perf-agent collect traces   --service <svc> --window 1h --ds-uid <tempo-uid>
   go-perf-agent collect profiles --service <svc> --window 1h --ds-uid <pyro-uid> --span-id <pyroscope.profile.id>
   # or: collect exemplars -> collect profiles --profile-id <uuid>. Else drop the flags for the service-wide profile.
   ```
   No gcx: `go-perf-agent collect local --pkg ./pkg --bench Name` (profiles-first) - ASK which codepath. UIDs come from `gcx datasources list`.

2. EXTRACT: `go-perf-agent hotspots` ranks editable symbols into `hotspots.json`. Only `editable:true` are candidates.

3. HYPOTHESIZE (parallel `gpa-analyst`, one per candidate). Each reads the symbol + `catalog/patterns.yaml` and returns one hypothesis (or null+reason). Collect them into `hypotheses.json`. Also send the top NON-editable vendored/generated hot symbols (they return `dependency` hypotheses). Skip sub-1% symbols (Amdahl). The optimization hierarchy and pattern matching live in `gpa-analyst`.

4. VALIDATE (`gpa-validation`, one hypothesis at a time - the verdict step is serial).
   ```bash
   go-perf-agent bench baseline <id>   # worktree + baseline binary. NEEDS_BENCHMARK -> author one, re-run.
   go-perf-agent bench verdict  <id>   # tests -> interleaved A/B benchmark -> benchstat gate
   ```
   Apply EXACTLY ONE change in the worktree and stage it (`git -C .go-perf-agent/wt/<id> add -A`) so the authored benchmark ships in the patch. The gate decides. The verdict lands in `runs/<id>/verdict.json` (numbers nested under `.verdict`, not the root).

5. CRITIQUE + REPORT. `gpa-critic` vets each proved change for behavior-preservation / benchmark-gaming and can downgrade it. Then `go-perf-agent report` writes `report.md` (with telemetry-coverage gaps, and workload/dependency findings as advisory). Share each proved finding's benchstat table + its patch (`git -C .go-perf-agent/wt/<id> diff HEAD`, which includes the staged benchmark).

6. VERIFY IN PROD (always tell the user). A PROVED verdict is a local win, not proof. Ship behind a flag, re-pull the same profile/traces, and confirm the hot symbol shrank (`go tool pprof -diff_base`). For a gc-axis win, confirm with `GODEBUG=gctrace=1` and the GOGC/GOMEMLIMIT levers. Never present a proved hypothesis as done.

## Measurement discipline

Baseline setup can parallelize, but `bench verdict` MUST run serially - concurrent benchmarks contend
for CPU and defeat the run-by-run interleaving the gate relies on. The engine enforces this with an
exclusive `bench.lock` (fail-fast), but still schedule them one at a time. Measure on an idle machine on AC power with the browser/IDE/video closed. `GPA_BENCH_COUNT` defaults to 10 (benchstat's floor) -  raise it for smaller deltas.

## Cleanup

`go-perf-agent clean` removes the per-hypothesis worktrees (`--all` also wipes artifacts, keeping `scope.json`). Keep proved worktrees until the user has cherry-picked them.

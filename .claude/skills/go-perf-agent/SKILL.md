---
name: go-perf-agent
description: LLM-assisted Go performance audit agent for the Grafana LGTM stack. Forms optimization hypotheses from real Tempo traces + Pyroscope profiles (via gcx) and the common-Go-perf-pattern catalog, validates each in an isolated git worktree with interleaved benchmarks, and proves or rejects with benchstat. Use when asked to audit a Go codebase for performance and improve it from production telemetry.
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent, AskUserQuestion
---

# go-perf-agent

LLM-assisted Go performance audit over the Grafana LGTM stack. A hybrid system: deterministic
tools do all telemetry collection and measurement; the LLM only reasons about code (forming
hypotheses, applying one change, authoring a missing benchmark). Hard numbers from `benchstat`
decide keep/reject - never the model. See `DECISIONS.md` for why.

Findings are LLM-assisted hypotheses. A PROVED verdict means "worth shipping behind a flag and
measuring", NOT "proven". Always tell the user to validate each accepted change in production
against real traffic and the same telemetry before trusting it.

The CLI is the `go-perf-agent` binary (a single Go program; build with `go build -o
go-perf-agent .` from this repo and put it on PATH). Run it from the target Go module root.
Working state lives in `.go-perf-agent/` (gitignored).

Config (env): `GPA_BENCH_COUNT` (=6, interleave rounds) · `GPA_ALPHA` (=0.05, benchstat
significance) · `GPA_PYRO_DS` · `GPA_TEMPO_DS_UID` · `GPA_DIR` (=.go-perf-agent).

## The loop

```
COLLECT -> EXTRACT -> HYPOTHESIZE -> VALIDATE (per worktree) -> REPORT -> VERIFY IN PROD
 (tools)   (tools)     (LLM+catalog)  (tools measure, LLM edits)  (tools)   (user)
```

## Agents (in `.claude/agents/`)

You are the orchestrator - drive the loop below and spawn these four specialists (there is no
separate control agent; this skill is the controller):

- `gpa-query-telemetry` - finds WHERE it is slow (Tempo/Pyroscope via gcx, or local pprof when
  gcx is absent); asks the user for service/window/UIDs or a target function. Stage: COLLECT.
- `gpa-analyst` - one per candidate hotspot; locates it in source, understands why it is hot,
  and forms a testable hypothesis (or null). Stages: EXTRACT + HYPOTHESIZE.
- `gpa-validation` - authors benchmark, applies one change, runs the gate; sets `proved` /
  `rejected` / `need_more_data`. Stage: VALIDATE.
- `gpa-critic` - structurally distinct reflexion pass; reviews each `proved` change for
  behavior-preservation / benchmark-gaming and can downgrade it. Stage: CRITIQUE.

Other entry points: `go-perf-agent target-diff` (review a PR / local diff - changed funcs become
the candidate set), `go-perf-agent bench-regression` (base-vs-head regression check, no edit),
`go-perf-agent eval` (run the golden scenarios to check the engine itself).

## Step 0 - preflight

```bash
go-perf-agent selftest        # offline: proves the pipeline runs without Grafana
gcx auth login           # ONLY if collecting live telemetry and the session is expired
```

If the user has not picked a target service/window, ASK (AskUserQuestion). Do not guess.

## Step 1 - COLLECT (deterministic)

LGTM path (gcx set up + `gcx auth login`):
```bash
go-perf-agent collect-profiles --service <svc> --window 1h     # pyroscope cpu+alloc leaderboards
go-perf-agent collect-traces   --service <svc> --window 1h --ds-uid <tempo-ds-uid>   # optional
```
`gcx datasources tempo query` is not implemented; traces go through the datasource proxy, which
needs the Tempo datasource UID (`gcx datasources list`). Profiles are the primary code-level
signal; traces only localize which operation is slow.

Local fallback (gcx not set up / not authed) - profile with go pprof, no Grafana:
```bash
go-perf-agent collect-local --pkg ./path/to/pkg --bench BenchmarkName   # writes cpu+alloc profiles
# or drop an existing profile in: cp their.prof .go-perf-agent/profiles/
```
In the local case, ASK the user which codepath/package/function to target - that focuses
profiling and scope. `gpa-query-telemetry` owns picking LGTM vs local and the target question.

## Step 2 - EXTRACT (deterministic)

```bash
go-perf-agent hotspots        # -> .go-perf-agent/hotspots.json: ranked editable symbols + package
```

Only `editable:true` symbols (in this module, not stdlib/vendor) are candidates.

## Step 3 - HYPOTHESIZE (LLM + catalog) - this is your job

For each top editable hotspot, read the symbol's source (resolve `file:line` with Grep/Read)
and match it against `go-perf-agent/catalog/patterns.yaml`. The catalog's `detect` regexes
pre-filter which patterns are even plausible; you make the judgement call among them.

Produce `.go-perf-agent/hypotheses.json` - an array conforming to
`go-perf-agent/schema/hypothesis.schema.json`. One hypothesis = one symbol + one pattern + one
benchmark that can prove it. Rules:

- Tie every hypothesis to a real signal (the hotspot's weight + metric). No signal, no
  hypothesis.
- Pick `metric` = the benchstat metric that should move (`ns_op`/`B_op`/`allocs_op`),
  matching the pattern's `optimizes`.
- If no benchmark exercises the symbol, set `benchmark.needs_authoring: true` and name the
  package; you will author it in the worktree during validation.
- Prefer low-`risk` patterns first. Skip anything in generated/vendored code.
- Delegate the per-symbol analysis to parallel `gpa-analyst` agents (one per candidate
  hotspot); collect their structured objects into the array, dropping nulls.

## Step 4 - VALIDATE (tools measure, you edit) - one hypothesis at a time

For each hypothesis id:

```bash
go-perf-agent bench-baseline <id>        # creates .go-perf-agent/wt/<id> worktree + compiles baseline binary
```

- If it prints `NEEDS_BENCHMARK: ...`, write a benchmark (and a correctness `Test...` if none
  covers the symbol) in the worktree package, then re-run `bench-baseline`. The benchmark must
  follow the existing benchmark style in that package (read the package's `*_test.go` first and
  match its conventions), exercise the hot path at a representative size, and call
  `b.ReportAllocs()`. If you cannot write a faithful benchmark, mark the hypothesis
  `need_more_data`.
- Apply EXACTLY ONE change in `.go-perf-agent/wt/<id>/` - the transform from the pattern. Do not
  batch changes; the verdict must be attributable. Keep the diff minimal.
- After the verdict, stage the worktree (`git -C .go-perf-agent/wt/<id> add -A`) so the authored
  benchmark+test ship inside the patch – an untracked benchmark is invisible to `git diff`, and a
  patch without it can't be re-run to prove the gain.

```bash
go-perf-agent bench-verdict <id>         # tests -> interleaved A/B benchmark -> benchstat gate
```

The gate (pure, no model input): PROVED iff correctness tests pass AND the proof metric shows
a statistically significant improvement (benchstat, p<alpha) AND no other metric
significantly regresses; REJECTED otherwise; NEED_MORE_DATA when it cannot be honestly tested
locally (no faithful benchmark, within-noise, or needs prod data). The verdict + benchstat
table land in `.go-perf-agent/runs/<id>/verdict.json`.

Interleaved A/B (baseline and candidate binaries alternated run-by-run) is what makes the
verdict trustworthy on a noisy laptop - do not replace it with two separate `go test` runs.

## Step 5 - when blocked, ASK (do not guess)

Use AskUserQuestion when: a hypothesis needs a benchmark you can't safely write (unclear
representative input), tests fail in a way that looks pre-existing, the hotspot is in
generated/vendored code, or the signal is ambiguous. Surface the specific blocker.

## Step 6 - REPORT

```bash
go-perf-agent report                     # -> .go-perf-agent/report.md
```

Summarize for the user: proved hypotheses (with worktree paths), rejected ones with the reason,
and need_more_data ones with what input you need. For each proved finding, SHARE THE PROOF: the
full benchstat table from the microbenchmark runs (baseline vs candidate, all metrics + p) AND a
patch that includes the authored benchmark - inspect/extract it with
`git -C .go-perf-agent/wt/<id> diff HEAD` (plain `git diff` omits the staged-but-new benchmark
file). A finding handed back without its benchmark and its numbers is not reviewable. Proved
worktrees are left intact (and staged) so the user can review and cherry-pick.

## Step 7 - VERIFY IN PRODUCTION (always state this)

A PROVED verdict is a local-benchmark win, not proof. Local benchmarks miss real input
distributions, production hardware, cache/GC behaviour under load, and concurrency. For every
proved hypothesis, tell the user to:
1. ship the change behind a flag / canary,
2. re-pull the SAME Pyroscope profile and Tempo traces for the service after rollout, and
3. confirm the hot symbol's weight actually dropped and latency/alloc moved the predicted way,
   with no regression elsewhere.
Only then is the finding confirmed. Never present a proved hypothesis as "done".

## Parallel validation

Validation parallelizes cleanly because each hypothesis works in its own
`.go-perf-agent/wt/<id>` worktree. Spawn the `gpa-validation` agents concurrently (one message,
multiple agent calls) rather than serially. No special runtime is needed - the isolation is the
worktree, and the gate is the `go-perf-agent` binary.

## Cleanup

`git worktree remove .go-perf-agent/wt/<id>` for rejected/abandoned ones. Keep proved ones until
the user has cherry-picked the change.

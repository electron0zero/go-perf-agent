# go-perf-agent

go-perf-agent finds where a Go service is slow from real telemetry, proposes one optimization at a
time, and reports it as proven only when [`benchstat`](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat)
says so. You get a short, grounded list worth shipping instead of speculation.

It is a hybrid: a single Go binary does the deterministic work (collect telemetry, rank hot code,
run an interleaved benchmark gate), and a [Claude Code](https://docs.claude.com/en/docs/claude-code)
skill plus four agents do the reasoning (form a hypothesis, apply one change, author a missing
benchmark). Hard numbers decide keep or reject, never the model.

Data comes from the Grafana stack via the [`gcx`](https://github.com/grafana/gcx) CLI:
[Tempo](https://github.com/grafana/tempo) traces find the slow operation, then
[Pyroscope](https://github.com/grafana/pyroscope) profiles explain it at the code level. With no
`gcx`, it falls back to a local [`go pprof`](https://pkg.go.dev/runtime/pprof) profile.

> [!NOTE]
> Alpha-stage software, provided as-is with no warranty. Use at your own risk. A personal project, not affiliated with, or endorsed by, my employer.

## How it works

The skill orchestrates; four agents reason; the Go binary does the deterministic work. They
connect through files under `.go-perf-agent/`, not direct messages.

```mermaid
flowchart TD
    user([USER]) --> rank["RANK<br/>prod: traces find the slow operation, then profiles for it (via gcx).<br/>local or PR diff: profile / changed funcs. builds ranked hotspots"]
    rank --> hyp["HYPOTHESIZE<br/>one hypothesis per code hotspot"]
    hyp -->|"per hotspot"| hyp
    hyp --> val["VALIDATE<br/>one change in a worktree.<br/>each hypothesis is proved, rejected, or needs more data"]
    val -->|"per hypothesis"| val
    val --> critic["CRITIQUE<br/>proved hypotheses only.<br/>can only downgrade"]
    critic --> report["REPORT<br/>.go-perf-agent/report.md"]
    report --> ship([USER ships and verifies in prod])

    classDef step fill:#dae8fc,stroke:#6c8ebf,color:#000;
    class rank,hyp,val,critic,report step;
```

`bench-regression` (base-vs-head) and `eval` (golden scenarios) are separate entry points, not shown.

## How to use

Prerequisites - tools on PATH:

| tool | version | needed for |
|---|---|---|
| `go` | 1.23+ | building/running go-perf-agent and the benchmarks |
| `git` | 2.5+ (worktree support) | per-hypothesis worktrees, diff modes |
| `benchstat` | latest (`go install golang.org/x/perf/cmd/benchstat@latest`) | the numeric gate |
| `gcx` | **v0.4.2+** | production telemetry (optional; local profiling works without it) |
| `gh` | latest | PR diff mode only (`target-diff --pr`) |

gcx minimum: v0.4.2 or newer, authenticated (`gcx auth login`). Older builds (e.g. v0.1.x) are not
enough - the production path needs `gcx datasources tempo query` (traces), `gcx datasources
pyroscope exemplars` (the trace->profile pivot), and `gcx datasources pyroscope query -o pprof`
(profiles), none of which exist in early versions. Install/upgrade with
`go install github.com/grafana/gcx/cmd/gcx@latest` (building gcx itself currently needs Go 1.26+).
No gcx? The local `collect-local` path profiles with `go pprof` and needs none of the above.

```bash
go build -o go-perf-agent .     # or: go install .
```

Recommended: run it as an agent. Load this repo's `.claude/` (run Claude Code from here, or copy
`.claude/skills/go-perf-agent` and `.claude/agents/gpa-*.md` into the target repo or `~/.claude/`),
then invoke the `go-perf-agent` skill from the target module root. It asks what to audit, drives the
loop, and writes `.go-perf-agent/report.md`. See `.claude/skills/go-perf-agent/SKILL.md` for the
full loop, gate, and config.

## Use cases

The same gate runs from three starting points.

### 1. A production service (traces-first)

Tell the skill the service and window, e.g. "audit `tempo-ingester` over the last 1h, scope
`pkg/parquet` and `tempodb`". By hand, production goes traces-first: find the slow operation, then
profile that work.

```bash
go-perf-agent scope --include "pkg/parquet,tempodb" --exclude "vendor"
go-perf-agent collect-traces    --service tempo-ingester --window 1h --ds-uid <tempo-uid>   # 1. slowest operations
go-perf-agent collect-exemplars --service tempo-ingester --window 1h --ds-uid <pyro-uid>    # 2. profile UUIDs for that work
go-perf-agent collect-profiles  --service tempo-ingester --window 1h --ds-uid <pyro-uid> --profile-id <uuid>   # 3. pprof
go-perf-agent hotspots                                                                       # ranked candidates
#   form hypotheses (skill/agents) -> validate each -> report
go-perf-agent report
```

If exemplars come back empty (no span-aware instrumentation), drop `--profile-id` and pull the
service-wide profile; the trace step still localized the slow service. After a proved change ships
behind a flag, re-run the same queries and confirm the hot symbol's weight dropped in production.

### 2. A GitHub PR

Optimize the code the PR touched and check it did not make a changed function slower.

```bash
go-perf-agent target-diff --pr https://github.com/org/repo/pull/123   # triage: changed funcs -> candidates (reads the patch via gh)
gh pr checkout 123                                                    # to optimize, check the PR out (validation edits a worktree)
go-perf-agent target-diff --base main                                 # changed funcs -> candidates + scope
go-perf-agent bench-regression --pkg ./pkg/x --bench BenchmarkY --base main   # regressed? -> REGRESSION | CLEAN | INCONCLUSIVE
```

### 3. A local diff (work in progress)

The PR case for your own changes, before you open a PR.

```bash
go-perf-agent target-diff                 # default: working-tree changes vs HEAD
go-perf-agent target-diff --base main     # or: your branch's commits vs main
go-perf-agent bench-regression --pkg ./pkg/x --bench BenchmarkY --base main   # optional regression check
```

No `gcx`? Profile locally instead of the trace/profile steps: `go-perf-agent collect-local --pkg
./pkg/x --bench BenchmarkY`, then `hotspots`. Local is the only profiles-first path.

## Things to keep in mind

- Every finding is a hypothesis. A PROVED verdict is a local-benchmark win, not truth: production
  has different hardware, inputs, and load. Always re-check the same telemetry in production before
  trusting a change.
- Results are only as good as the machine. A noisy laptop widens confidence intervals and pushes
  borderline wins to `need_more_data`; run on an idle machine connected to power (not on battery,
  where CPU throttling skews timings) for the most stable results.
- Without `scope`, the whole codebase is in play. Use `--include`/`--exclude` to keep the agents off
  vendored, generated, or frozen packages.
- Changes happen in throwaway worktrees under `.go-perf-agent/wt/`; proved ones are left for you to
  review (`git -C <wt> diff`) and cherry-pick.
- External tools must be on PATH: `go`, `benchstat`, `git`, `gh`, and `gcx`.

## Acknowledgements

Built with open source:
- [Tempo](https://github.com/grafana/tempo),
- [Pyroscope](https://github.com/grafana/pyroscope)
- [`gcx`](https://github.com/grafana/gcx) CLI
- Go's [`pprof`](https://pkg.go.dev/runtime/pprof), [`benchstat`](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat),
- [`alecthomas/kong`](https://github.com/alecthomas/kong) for CLI flags,

The performance pattern catalog is built from:
- Dave Cheney's High Performance Go Workshop: https://dave.cheney.net/high-performance-go-workshop/dotgo-paris.html
- Bryan Boreham's fork: https://github.com/bboreham/high-performance-go-workshop
- The Go wiki performance guide: https://go.dev/wiki/Performance
- The Go optimization guide (goperf.dev): https://goperf.dev/

## Further reading

- Dave Cheney's High Performance Go Workshop: https://dave.cheney.net/high-performance-go-workshop/dotgo-paris.html
- Go wiki performance guide: https://go.dev/wiki/Performance
- Go optimization guide (goperf.dev): https://goperf.dev/
- "Profiling Go Programs" (the pprof blog): https://go.dev/blog/pprof
- Eben Freeman on the garbage collector: https://youtu.be/M0HER1G5BRw
- Bryan Boreham, "Obscure Go Optimisations": https://youtu.be/rRtihWOcaLI
- Damian Gryski's go-perfbook: https://github.com/dgryski/go-perfbook/blob/master/performance.md#writing-and-optimizing-go-code
- Guide to the Go GC: https://go.dev/doc/gc-guide

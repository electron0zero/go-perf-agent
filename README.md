# go-perf-agent

An LLM-assisted agent and tooling that audits a Go codebase for performance and proposes optimizations in go codebases.

It can pull data like traces from Grafana Tempo and profiles from Grafana Pyroscope (via the `gcx` CLI),
or a local `go pprof` profile when Tempo or Pyroscope is not set up.

With the data, it ranks the hot code, forms hypotheses against by using a catalog of common Go performance patterns,
and tests each one in an isolated git worktree with benchmarks. 

A change is only reported as proven when `benchstat` says so.

The tool is a single Go binary, and the core loop is a Claude Code skill plus four agents for the steps

Why it exists: performance work should start from a real signal and end with a measured result.
The agent grounds every suggestion in a profile or trace and gates every change behind a
statistical benchmark, so you get a short list of changes worth shipping instead of a wall of
speculative advice.

## How it connects

The skill orchestrates; four agents do the reasoning; the Go binary does the deterministic work.
They connect through files under `.go-perf-agent/` (the source of truth), not direct messages.

```mermaid
flowchart TD
    user([USER]) --> skill[["skill (orchestrator)"]]

    %% COLLECT + EXTRACT - codebase-wide telemetry is the core; a diff is the alternate
    skill --> qt(["gpa-query-telemetry"])
    skill -. alt .-> diff["target-diff<br/>PR / local diff"]
    qt -->|"gcx LGTM or local pprof"| prof[/"profiles/"/]
    prof --> hsCmd["hotspots"]
    hsCmd --> hs[("hotspots.json<br/>ranked candidates")]
    diff --> hs

    %% HYPOTHESIZE - one analyst per candidate, in parallel
    hs --> an(["gpa-analyst x N"])
    an --> hyp[("hypotheses.json")]

    %% VALIDATE - one validation per hypothesis, each in its own worktree
    hyp --> val(["gpa-validation x N"])
    val --> bb["bench-baseline"]
    bb --> edit["ONE change in wt/&lt;id&gt;"]
    edit --> bv["bench-verdict"]
    bv --> gates{"gates: structural ·<br/>correctness · benchstat"}
    gates --> verdict[("runs/&lt;id&gt;/verdict.json<br/>proved | rejected | need_more_data")]

    %% CRITIQUE - proved only, can only downgrade
    verdict -->|proved| critic(["gpa-critic<br/>downgrade-only"])
    critic --> report["report"]
    report --> rep[("report.md")]
    rep --> ship([USER ships behind a flag])
    ship --> prod([verify in prod])

    classDef agent fill:#dae8fc,stroke:#6c8ebf,color:#000;
    classDef cmd fill:#d5e8d4,stroke:#82b366,color:#000;
    classDef file fill:#fff2cc,stroke:#d6b656,color:#000;
    class qt,an,val,critic agent;
    class diff,hsCmd,bb,bv,report cmd;
    class prof,hs,hyp,verdict,rep file;
```

Blue = LLM agent · green = Go binary command · yellow = `.go-perf-agent/` file (how stages
connect). `bench-regression` (base-vs-head) and `eval` (golden scenarios) are separate entry
points, not shown.

## How to use

Prerequisites: Go 1.23+, [`benchstat`](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat)
(`go install golang.org/x/perf/cmd/benchstat@latest`), and `git`. For production telemetry,
install and authenticate [`gcx`](https://github.com/grafana/gcx) (`gcx auth login`). gcx is
optional - without it the agent profiles locally.

Build:

```bash
go build -o go-perf-agent .     # or: go install .
```

Run it as an agent (recommended): load this repo's `.claude/` (run Claude Code from here, or
copy/symlink `.claude/skills/go-perf-agent` and `.claude/agents/gpa-*.md` into the target repo
or `~/.claude/`), then invoke the `go-perf-agent` skill from the target module root. The skill
asks you for the codebase path, what is in/out of scope, and the service or target function, then
drives the loop, and writes `.go-perf-agent/report.md`.

Or drive the stages by hand from the target module root:

```bash
go-perf-agent scope --include "pkg/parquet,tempodb" --exclude "vendor"   # focus the audit
go-perf-agent collect-profiles --service my-svc --window 1h              # LGTM (needs gcx auth)
#   no gcx? profile locally instead:
go-perf-agent collect-local --pkg ./pkg/parquet --bench BenchmarkDecode
go-perf-agent hotspots                                                   # rank in-scope hotspots
#   form hypotheses (the skill/agents, or hand-write .go-perf-agent/hypotheses.json)
go-perf-agent bench-baseline h-001-... && go-perf-agent bench-verdict h-001-...
go-perf-agent report
```

See `.claude/skills/go-perf-agent/SKILL.md` for the full loop, the agents, scope, the
PROVED/REJECTED/NEED_MORE_DATA gate, and configuration.

## When to use

Good fits:
- You run a Go service with Pyroscope/Tempo telemetry and want code-level perf wins backed by real data.
- You have a hot package or function (or a local profile) and want hypotheses tested, not just listed.
- You want to scope an audit to part of a large repo and keep it off vendored/generated code.

Not a fit:
- Micro-tuning with no signal
- A replacement for production validation - a local benchmark win is a starting point, not proof.

## Warnings & gotchas

- LLM-assisted: every finding is a hypothesis. A PROVED verdict is a local-benchmark win, not
  truth. always re-check the same telemetry in production before trusting it.
- Local benchmarks can mislead: production hardware, input distributions, load, and concurrency
  differ. The agent interleaves baseline/candidate runs to cancel machine noise, but that does
  not substitute for a production check.
- Nothing is applied to your tree automatically. Changes are made in throwaway git worktrees
  under `.go-perf-agent/wt/`; proved ones are left for you to review (`git -C <wt> diff`) and
  cherry-pick.
- Scope it. Without `scope`, the whole module is in play. Use `--include`/`--exclude` to keep the
  agents on the code you care about and off vendored, generated, or frozen packages.
- gcx auth is needed for production telemetry. Without it the agent profiles locally and will ask
  you for a target package/function (and a benchmark, which it can author).
- External tools must be on PATH: `go`, `benchstat`, `git`, and `gcx` (for the LGTM path).
- Benchmark results are only as good as the machine. A noisy laptop widens confidence intervals
  and pushes borderline wins to `need_more_data`; run on an idle machine for tight results.

## Acknowledgements

Built on the Grafana LGTM stack and Pyroscope, the [`gcx`](https://github.com/grafana/gcx) CLI,
Go's `pprof` and [`benchstat`](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat), and
[`alecthomas/kong`](https://github.com/alecthomas/kong).

## Credits

The Go performance pattern catalog is built from Dave Cheney's High Performance Go Workshop and  Bryan Boreham's workshop code fork:
- https://dave.cheney.net/high-performance-go-workshop/dotgo-paris.html
- https://github.com/bboreham/high-performance-go-workshop

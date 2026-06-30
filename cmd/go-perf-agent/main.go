// Command go-perf-agent is the deterministic engine of the go-perf-agent loop: collect telemetry,
// rank hotspots, set up worktrees, run interleaved benchmarks, and gate with benchstat. The LLM
// stages live in the Claude skill/agents under .claude/; the reusable libraries live in internal/.
//
// Findings are hypotheses: always validate an accepted change in production before trusting it.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alecthomas/kong"
)

// config (env-overridable)
var (
	gpaDir     = env("GPA_DIR", ".go-perf-agent")
	benchCount = envInt("GPA_BENCH_COUNT", 6) // interleave rounds for statistical significance
	alpha      = env("GPA_ALPHA", "0.05")     // benchstat significance threshold
	minImprove = envFloat("GPA_MIN_IMPROVEMENT", 3.0)
	regressTol = envFloat("GPA_REGRESSION_TOL", 2.0)
	cpuPT      = env("GPA_CPU_PT", "process_cpu:cpu:nanoseconds:cpu:nanoseconds")
	allocPT    = env("GPA_ALLOC_PT", "memory:alloc_space:bytes:space:bytes")
	inusePT    = env("GPA_INUSE_PT", "memory:inuse_space:bytes:space:bytes") // resident heap - the OOM signal
	pyroDS     = env("GPA_PYRO_DS", "")
	tempoDSUID = env("GPA_TEMPO_DS_UID", "")

	modulePath string // from go.mod
)

// cli is the kong command tree. Each leaf is a *Cmd struct with a Run() error method (same
// pattern as cmd/tempo-cli). kong derives kebab-case command names from the field names.
var cli struct {
	Check        checkCmd        `cmd:"" help:"Preflight: check required tools + gcx capabilities/min version, warn on gaps"`
	Scope        scopeCmd        `cmd:"" help:"Set in/out-of-scope code paths for the agents"`
	Collect      collectCmd      `cmd:"" help:"Collect telemetry (traces/exemplars/profiles) or profile a local benchmark"`
	TraceSummary traceSummaryCmd `cmd:"" help:"Extract request shape + span fan-out from a dumped trace (for the agent to interpret)"`
	Hotspots     hotspotsCmd     `cmd:"" help:"Rank hot symbols and map to packages -> hotspots.json"`
	TargetDiff   targetDiffCmd   `cmd:"" help:"Target a PR / local diff: changed funcs become the candidate set"`
	Bench        benchCmd        `cmd:"" help:"Benchmark gate: baseline, verdict, regression"`
	Critic       criticCmd       `cmd:"" help:"Record the reflexion critic's judgment"`
	Validate     validateCmd     `cmd:"" help:"Baseline + patch + verdict for one hypothesis (or --all to set up baselines)"`
	Report       reportCmd       `cmd:"" help:"Write report.md from the per-hypothesis verdicts"`
}

func main() {
	ctx := kong.Parse(&cli,
		kong.Name("go-perf-agent"),
		kong.Description("Deterministic engine for the go-perf-agent loop: collect telemetry, rank hotspots, run the benchstat gate. Findings are hypotheses - validate each in production before trusting it.\n\nEasiest start: run the go-perf-agent skill from your module root and let it drive. By hand, run 'go-perf-agent check' (preflight), then for production telemetry go traces-first: collect traces -> collect exemplars -> collect profiles -> hotspots. No gcx? use collect local (profiles-first).\n\nEnv: GPA_BENCH_COUNT(=6) GPA_ALPHA(=0.05) GPA_PYRO_DS GPA_TEMPO_DS_UID GPA_DIR(=.go-perf-agent)"),
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{Compact: true}),
	)
	// every command operates on a target Go module
	if _, err := os.Stat("go.mod"); err != nil {
		die("run from a Go module root (no go.mod here)")
	}
	loadModule()
	ctx.FatalIfErrorf(ctx.Run())
}

// ---- helpers ----------------------------------------------------------------------------

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envFloat(k string, def float64) float64 {
	if v := os.Getenv(k); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func die(msg string) {
	fmt.Fprintln(os.Stderr, "go-perf-agent: "+msg)
	os.Exit(1)
}

func info(format string, a ...any) {
	fmt.Fprintln(os.Stderr, "go-perf-agent: "+fmt.Sprintf(format, a...))
}

func loadModule() {
	b, err := os.ReadFile("go.mod")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "module ") {
			modulePath = strings.TrimSpace(strings.TrimPrefix(line, "module "))
			return
		}
	}
}

func ensureDirs() {
	for _, d := range []string{"profiles", "traces", "runs", "wt"} {
		_ = os.MkdirAll(filepath.Join(gpaDir, d), 0o755)
	}
}

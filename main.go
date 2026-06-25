// Command go-perf-agent is the deterministic engine of the go-perf-agent loop: it collects
// telemetry (via gcx), ranks hotspots, sets up isolated worktrees, runs interleaved benchmarks,
// and decides PROVED / REJECTED with benchstat. The LLM-only stages (forming hypotheses,
// applying the one code change, authoring a missing benchmark) are driven by the go-perf-agent
// skill/agents, which slot in between bench-baseline and bench-verdict.
//
// LLM-ASSISTED: findings are hypotheses. Always validate each accepted change in production
// against real traffic before trusting it - a local benchmark win is necessary, not sufficient.
package main

import (
	"fmt"
	"os"
	"os/exec"
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
	Selftest        selftestCmd        `cmd:"" help:"Offline pipeline check (no Grafana): real bench -> hotspots"`
	Scope           scopeCmd           `cmd:"" help:"Set in/out-of-scope code paths for the agents"`
	CollectProfiles collectProfilesCmd `cmd:"" help:"Pull pyroscope cpu+alloc leaderboards via gcx [needs auth]"`
	CollectTraces   collectTracesCmd   `cmd:"" help:"Pull slow-span traces via the tempo datasource proxy [needs auth]"`
	CollectLocal    collectLocalCmd    `cmd:"" help:"Profile a benchmark locally with go pprof (no Grafana needed)"`
	Hotspots        hotspotsCmd        `cmd:"" help:"Rank hot symbols and map to packages -> hotspots.json"`
	TargetDiff      targetDiffCmd      `cmd:"" help:"Target a PR / local diff: changed funcs become the candidate set"`
	BenchBaseline   benchBaselineCmd   `cmd:"" help:"Create worktree + compile the pristine baseline binary"`
	BenchVerdict    benchVerdictCmd    `cmd:"" help:"Tests + interleaved benchmark + benchstat gate (PROVED/REJECTED)"`
	BenchRegression benchRegressionCmd `cmd:"" help:"Compare a benchmark base-vs-head for a regression (no edit)"`
	Critic          criticCmd          `cmd:"" help:"Record the reflexion critic's judgment (can only downgrade a PROVED)"`
	Validate        validateCmd        `cmd:"" help:"Baseline, apply a patch, then verdict (non-LLM path)"`
	ValidateAll     validateAllCmd     `cmd:"" help:"Set up baselines for every hypothesis"`
	Report          reportCmd          `cmd:"" help:"Write report.md from the per-hypothesis verdicts"`
	Eval            evalCmd            `cmd:"" help:"Run golden scenarios N times and check the engine's verdicts (flakiness-aware)"`
}

func main() {
	ctx := kong.Parse(&cli,
		kong.Name("go-perf-agent"),
		kong.Description("LLM-assisted Go performance audit over the Grafana LGTM stack. Findings are hypotheses - validate each in production before trusting it.\n\nEnv: GPA_BENCH_COUNT(=6) GPA_ALPHA(=0.05) GPA_PYRO_DS GPA_TEMPO_DS_UID GPA_DIR(=.go-perf-agent)"),
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

// run executes name in dir (empty = cwd) and returns stdout, stderr, error.
func run(dir, name string, args ...string) (string, string, error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

func mustAbs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// benchPkgRel normalizes a benchmark pkg to a worktree-relative dir to cd into: a trailing
// "..." is a valid `go test` wildcard but not a real directory ("./..." -> module root "").
func benchPkgRel(pkg string) string {
	p := strings.TrimPrefix(pkg, "./")
	return strings.TrimRight(strings.TrimSuffix(p, "..."), "/")
}

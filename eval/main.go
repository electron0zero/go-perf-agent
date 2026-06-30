// Command gpa-eval runs the golden scenarios in eval/scenarios against a freshly built
// go-perf-agent binary and grades the engine's verdicts - the tool's own regression harness, not a
// user command. Run it via `go run ./eval` (the Makefile `eval` target). It self-builds the engine,
// so no prior `make build` is needed.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/alecthomas/kong"

	"go-perf-agent/internal/sh"
)

var cli struct {
	Dir        string `default:"eval/scenarios" help:"scenarios directory"`
	Runs       int    `default:"3" help:"runs per scenario (to catch flakiness)"`
	Only       string `help:"only run scenarios whose name contains this"`
	BenchCount int    `default:"10" help:"GPA_BENCH_COUNT for each scenario run"`
	GpaDir     string `name:"gpa-dir" env:"GPA_DIR" default:".go-perf-agent" help:"engine working-dir name (matches the engine's GPA_DIR)"`
}

func main() {
	kong.Parse(&cli, kong.Name("gpa-eval"),
		kong.Description("Golden-scenario regression harness for the go-perf-agent engine (dev/CI only). Self-builds the binary and grades its verdicts against known-correct scenarios."))

	bin, tmp, err := buildEngine()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gpa-eval:", err)
		os.Exit(1)
	}

	logf := func(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }
	runErr := Run(Opts{
		ScenariosDir: cli.Dir, Runs: cli.Runs, Only: cli.Only, BenchCount: cli.BenchCount, Self: bin, GpaDir: cli.GpaDir,
	}, logf)

	os.RemoveAll(tmp) // explicit: os.Exit below skips defers
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "gpa-eval:", runErr)
		os.Exit(1)
	}
}

// buildEngine compiles the go-perf-agent binary the scenarios run against into a temp dir, and
// returns its path plus the temp dir to clean up.
func buildEngine() (bin, tmp string, err error) {
	tmp, err = os.MkdirTemp("", "gpa-eval-bin-")
	if err != nil {
		return "", "", err
	}
	bin = filepath.Join(tmp, "go-perf-agent")
	if _, stderr, berr := sh.Run("", "go", "build", "-o", bin, "./cmd/go-perf-agent"); berr != nil {
		os.RemoveAll(tmp)
		return "", "", fmt.Errorf("build go-perf-agent: %s", stderr)
	}
	return bin, tmp, nil
}

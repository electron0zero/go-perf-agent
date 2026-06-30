// Command gpa-e2e runs go-perf-agent's end-to-end tool tests: `eval` (golden-scenario verdicts) and
// `smoke` (the collect->rank front-end). Dev/CI only, not a shipped command - run via `go run ./e2e`
// (the Makefile `e2e` target). Each subcommand self-builds the engine, so no prior `make build`.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alecthomas/kong"

	"go-perf-agent/internal/helper"
)

var cli struct {
	Eval  evalCmd  `cmd:"" help:"run the golden scenarios and grade the engine's verdicts"`
	Smoke smokeCmd `cmd:"" help:"build the engine and run collect-local -> hotspots on a fixture"`
}

func main() {
	ctx := kong.Parse(&cli, kong.Name("gpa-e2e"),
		kong.Description("go-perf-agent end-to-end tool tests (dev/CI only). eval: golden-scenario verdicts. smoke: the collect->rank front-end."))
	ctx.FatalIfErrorf(ctx.Run())
}

// buildEngine compiles the go-perf-agent binary the tests drive, into a temp dir; returns its path
// and the temp dir to clean up.
func buildEngine() (bin, tmp string, err error) {
	tmp, err = os.MkdirTemp("", "gpa-e2e-bin-")
	if err != nil {
		return "", "", err
	}
	bin = filepath.Join(tmp, "go-perf-agent")
	if _, stderr, berr := helper.Run("", "go", "build", "-o", bin, "./cmd/go-perf-agent"); berr != nil {
		os.RemoveAll(tmp)
		return "", "", fmt.Errorf("build go-perf-agent: %s", stderr)
	}
	return bin, tmp, nil
}

func logf(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }

// runSelf invokes the engine binary in dir with extra env, merging stdout+stderr for the log. Its
// own exec (not sh.Run) is needed to inject env and combine the streams.
func runSelf(self, dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command(self, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

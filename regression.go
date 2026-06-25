package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RegressionVerdict is the base-vs-head outcome (distinct from the hypothesis Verdict).
type RegressionVerdict struct {
	ID        string `json:"id"`
	Pkg       string `json:"pkg"`
	Bench     string `json:"bench"`
	BaseRef   string `json:"base_ref"`
	HeadRef   string `json:"head_ref"`
	Status    string `json:"status"` // regression | clean | inconclusive
	Reason    string `json:"reason"`
	Benchstat string `json:"benchstat"`
}

// bench-regression: did <head> get slower than <base> for <bench>? Builds the benchmark in a
// worktree at each ref, interleaves them, and reads benchstat with an inverted verdict - a
// significant positive delta on any metric in head is a REGRESSION. No code is edited.
type benchRegressionCmd struct {
	Pkg   string `required:"" help:"package holding the benchmark, e.g. ./pkg/parquet"`
	Bench string `required:"" help:"benchmark name (must exist in BOTH refs)"`
	Base  string `required:"" help:"base git ref, e.g. main"`
	Head  string `default:"HEAD" help:"head git ref"`
	ID    string `default:"regression" help:"label for the run/worktree dirs"`
}

func (c *benchRegressionCmd) Run() error {
	ensureDirs()
	runDir := filepath.Join(gpaDir, "runs", c.ID)
	_ = os.MkdirAll(runDir, 0o755)
	arun := mustAbs(runDir)
	rel := benchPkgRel(c.Pkg)

	baseBin, err := buildBenchAt(c.Base, c.ID+"-base", rel, filepath.Join(arun, "base.test"))
	if err != nil {
		return err
	}
	headBin, err := buildBenchAt(c.Head, c.ID+"-head", rel, filepath.Join(arun, "head.test"))
	if err != nil {
		return err
	}

	// both binaries must actually contain the benchmark, else we'd be comparing nothing
	if !benchExists(baseBin, c.Bench) {
		return writeRegression(c, "inconclusive", fmt.Sprintf("benchmark %s not present in base ref %s", c.Bench, c.Base), "", runDir)
	}
	if !benchExists(headBin, c.Bench) {
		return writeRegression(c, "inconclusive", fmt.Sprintf("benchmark %s not present in head ref %s", c.Bench, c.Head), "", runDir)
	}

	info("interleaved base(%s) vs head(%s): %d rounds of %s", c.Base, c.Head, benchCount, c.Bench)
	// pkgDir for running: use the head worktree's package dir (testdata etc. from head)
	headPkgDir := filepath.Join(gpaDir, "wt", c.ID+"-head", rel)
	csv, table := interleaveBenchstat(headPkgDir, baseBin, headBin, c.Bench, benchCount, runDir)

	// inverted verdict: any metric significantly WORSE in head (+) is a regression
	var regressions, improvements []string
	for _, m := range []string{"sec/op", "B/op", "allocs/op"} {
		d, p := parseBenchstat(csv, m)
		switch {
		case strings.HasPrefix(d, "+"):
			regressions = append(regressions, fmt.Sprintf("%s %s (p=%s)", m, d, p))
		case strings.HasPrefix(d, "-"):
			improvements = append(improvements, fmt.Sprintf("%s %s", m, d))
		}
	}

	status, reason := "clean", "no statistically significant regression"
	if len(regressions) > 0 {
		status = "regression"
		reason = "head is slower: " + strings.Join(regressions, ", ")
	} else if len(improvements) > 0 {
		reason = "no regression; improvements: " + strings.Join(improvements, ", ")
	}
	if err := writeRegression(c, status, reason, table, runDir); err != nil {
		return err
	}
	info("regression %s: %s - %s", c.ID, strings.ToUpper(status), reason)
	fmt.Fprint(os.Stderr, table)
	return nil
}

// buildBenchAt creates a detached worktree at ref and compiles the package's test binary.
func buildBenchAt(ref, wtName, relPkg, outBin string) (string, error) {
	wt := filepath.Join(gpaDir, "wt", wtName)
	if !fileExists(wt) {
		if _, stderr, err := run("", "git", "worktree", "add", "-q", "--detach", wt, ref); err != nil {
			return "", fmt.Errorf("git worktree add %s at %s failed: %s", wt, ref, stderr)
		}
	}
	pkgDir := filepath.Join(wt, relPkg)
	if _, stderr, err := run(pkgDir, "go", "test", "-c", "-o", outBin, "."); err != nil {
		return "", fmt.Errorf("compile bench at %s failed: %s", ref, stderr)
	}
	return outBin, nil
}

func benchExists(bin, bench string) bool {
	out, _, _ := run("", bin, "-test.run=^$", "-test.bench=^"+bench+"$", "-test.count=1")
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, bench) {
			return true
		}
	}
	return false
}

func writeRegression(c *benchRegressionCmd, status, reason, table, runDir string) error {
	v := RegressionVerdict{
		ID: c.ID, Pkg: c.Pkg, Bench: c.Bench, BaseRef: c.Base, HeadRef: c.Head,
		Status: status, Reason: reason, Benchstat: table,
	}
	if status == "inconclusive" {
		info("regression %s: INCONCLUSIVE - %s", c.ID, reason)
	}
	return writeJSON(filepath.Join(runDir, "regression.json"), v)
}

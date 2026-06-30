// Package gate is the benchmarking engine: it sets up per-hypothesis worktrees, compiles pristine
// baseline binaries, runs interleaved A/B benchmarks, and decides PROVED / REJECTED / NEED_MORE_DATA
// with benchstat. It also runs the base-vs-head regression check. The numeric gate decides; no model
// opinion enters keep/reject. logf takes progress (caller routes it; pass nil for silent).
package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go-perf-agent/internal/benchstat"
	"go-perf-agent/internal/hotspot"
	"go-perf-agent/internal/model"
	"go-perf-agent/internal/sh"
)

// Opts configures a single-hypothesis run. Dir is the .go-perf-agent working dir.
type Opts struct {
	ID         string
	Dir        string
	BenchCount int
	Alpha      string
	Patch      string // validate only: git patch applied in the worktree before the verdict
}

// Baseline creates the worktree and compiles the PRISTINE baseline test binary now, before any
// edit. A compiled binary is frozen against later source edits, so Verdict can interleave it
// against the candidate to cancel time-correlated machine noise. Returns the worktree path ("" when
// it wrote a non-worktree verdict, e.g. dependency opt-in needed).
func Baseline(o Opts, logf func(string, ...any)) (string, error) {
	logf = orNoop(logf)
	hyp, err := model.GetHypothesis(o.Dir, o.ID)
	if err != nil {
		return "", err
	}
	runDir := filepath.Join(o.Dir, "runs", o.ID)
	wt := filepath.Join(o.Dir, "wt", o.ID)
	_ = os.MkdirAll(runDir, 0o755)
	arun := mustAbs(runDir)

	// a dependency-change hypothesis only validates once the user opts its path into scope -
	// otherwise the structural gate would reject the edit anyway. Signal it cleanly instead.
	if needsDependencyOptIn(hyp, hotspot.LoadScope(o.Dir)) {
		logf("DEPENDENCY_OPT_IN: %s edits dependency %s; opt in with `go-perf-agent scope --include %s` then re-run", o.ID, hyp.Dependency.Path, hyp.Dependency.Path)
		_ = model.WriteJSON(filepath.Join(runDir, "verdict.json"), model.Verdict{
			ID: o.ID, Status: "need_more_data",
			Reason:  "dependency change needs opt-in: add " + hyp.Dependency.Path + " to scope, then re-run",
			Verdict: &model.VerdictDetail{Reason: "dependency change needs opt-in (" + hyp.Dependency.Path + ")"},
		})
		return "", nil
	}

	if exists(wt) {
		logf("worktree exists, reusing %s", wt)
	} else {
		logf("creating worktree %s off HEAD", wt)
		if _, stderr, err := sh.Run("", "git", "worktree", "add", "-q", "--detach", wt, "HEAD"); err != nil {
			return "", fmt.Errorf("git worktree add failed: %s", stderr)
		}
	}

	if hyp.Benchmark.NeedsAuthoring || hyp.Benchmark.Name == "" {
		// not a final verdict - signal the validation agent to author a benchmark, then re-run.
		logf("NEEDS_BENCHMARK: hypothesis %s needs a benchmark authored in %s (%s). The validation agent writes it, then re-runs bench-baseline.", o.ID, wt, hyp.Benchmark.Pkg)
		_ = model.WriteJSON(filepath.Join(runDir, "verdict.json"), model.Verdict{
			ID: o.ID, Status: "need_more_data", Reason: "benchmark needs authoring",
			Verdict: &model.VerdictDetail{Worktree: wt},
		})
		return wt, nil
	}

	pkgDir := filepath.Join(wt, benchPkgRel(hyp.Benchmark.Pkg))
	logf("compiling baseline binary for %s (%s)", hyp.Benchmark.Name, hyp.Benchmark.Pkg)
	if _, stderr, err := sh.Run(pkgDir, "go", "test", "-c", "-o", filepath.Join(arun, "baseline.test"), "."); err != nil {
		return "", fmt.Errorf("baseline compile failed for %s: %s", o.ID, stderr)
	}
	// snapshot the test files now (after any benchmark authoring) so Verdict can detect a candidate
	// that later edits the benchmark/test to game the result.
	_ = os.WriteFile(filepath.Join(runDir, "baseline-tests.sha"), []byte(testFilesHash(pkgDir)), 0o644)
	if len(hyp.Evidence) == 0 || string(hyp.Evidence) == "null" {
		logf("warning: hypothesis %s has no evidence - it should cite a real signal (profile/trace)", o.ID)
	}
	// smoke-run once so the user sees the starting numbers
	if out, _, err := sh.Run(pkgDir, filepath.Join(arun, "baseline.test"),
		"-test.run=^$", "-test.bench=^"+hyp.Benchmark.Name+"$", "-test.benchmem", "-test.count=1"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, hyp.Benchmark.Name) {
				logf("%s", line)
			}
		}
	}
	fmt.Println(wt) // worktree path is the command result (consumed by the caller/agent)
	return wt, nil
}

// testFilesHash hashes the package's *_test.go files so we can detect a candidate that edits the
// benchmark or correctness test it is being judged by.
func testFilesHash(pkgDir string) string {
	matches, _ := filepath.Glob(filepath.Join(pkgDir, "*_test.go"))
	sort.Strings(matches)
	h := sha256.New()
	for _, m := range matches {
		b, _ := os.ReadFile(m)
		h.Write([]byte(m))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// structuralGate enforces, in code (not prompt), that the candidate change is honest: it must not
// modify the benchmark/test being judged, and must stay inside scope. Returns a reject reason, or
// "" if the change is structurally allowed.
func structuralGate(dir, wt, pkgDir, runDir string) string {
	if sha, err := os.ReadFile(filepath.Join(runDir, "baseline-tests.sha")); err == nil {
		if testFilesHash(pkgDir) != strings.TrimSpace(string(sha)) {
			return "candidate modified the benchmark/test files - cannot change the ruler"
		}
	}
	sc := hotspot.LoadScope(dir)
	if sc == nil {
		return ""
	}
	out, _, _ := sh.Run(wt, "git", "status", "--porcelain")
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if i := strings.Index(path, " -> "); i >= 0 { // rename: take the new path
			path = path[i+4:]
		}
		if !strings.HasSuffix(path, ".go") {
			continue
		}
		if !hotspot.InScope(filepath.Dir(path), sc) {
			return "candidate edited out-of-scope file: " + path
		}
	}
	return ""
}

// Verdict is the numeric gate. Correctness tests, then INTERLEAVED A/B benchmarks (baseline and
// candidate binaries alternated run-by-run so drift hits both equally), then benchstat. PROVED iff
// tests pass AND significant improvement on the proof metric AND no other metric regresses.
func Verdict(o Opts, logf func(string, ...any)) error {
	logf = orNoop(logf)
	hyp, err := model.GetHypothesis(o.Dir, o.ID)
	if err != nil {
		return err
	}
	wt := filepath.Join(o.Dir, "wt", o.ID)
	runDir := filepath.Join(o.Dir, "runs", o.ID)
	arun := mustAbs(runDir)
	pkgDir := filepath.Join(wt, benchPkgRel(hyp.Benchmark.Pkg))
	baselineBin := filepath.Join(arun, "baseline.test")
	if !exists(wt) {
		return fmt.Errorf("no worktree for %s; run bench-baseline first", o.ID)
	}
	if !exists(baselineBin) {
		return fmt.Errorf("no baseline binary for %s; run bench-baseline first", o.ID)
	}

	// structural gate: refuse a dishonest change (gamed ruler / out-of-scope edit) before measuring
	if reason := structuralGate(o.Dir, wt, pkgDir, runDir); reason != "" {
		logf("  REJECTED (structural): %s", reason)
		return writeVerdict(o.Dir, o.ID, wt, false, false, "", "", reason)
	}

	// correctness gate first - a faster-but-wrong change is a rejection, not a win
	logf("tests: go test %s -count=1 (in worktree)", hyp.Benchmark.Pkg)
	if tout, terr, err := sh.Run(wt, "go", "test", hyp.Benchmark.Pkg, "-count=1"); err != nil {
		logf("  FAIL - correctness broken, rejecting")
		_ = os.WriteFile(filepath.Join(runDir, "tests.txt"), []byte(tout+terr), 0o644)
		return writeVerdict(o.Dir, o.ID, wt, false, false, "", "", "tests failed after change")
	}

	logf("compiling candidate binary")
	candidateBin := filepath.Join(arun, "candidate.test")
	if _, stderr, err := sh.Run(pkgDir, "go", "test", "-c", "-o", candidateBin, "."); err != nil {
		return fmt.Errorf("candidate compile failed for %s: %s", o.ID, stderr)
	}

	logf("interleaved benchmarking: %d rounds of %s", o.BenchCount, hyp.Benchmark.Name)
	csv, table := interleaveBenchstat(pkgDir, baselineBin, candidateBin, hyp.Benchmark.Name, o.BenchCount, runDir, o.Alpha)

	label := benchstat.ProofLabel(hyp.Metric)
	if label == "" {
		return fmt.Errorf("unknown metric %s", hyp.Metric)
	}
	kept, delta, p, reason := decideVerdict(csv, label)

	if err := writeVerdict(o.Dir, o.ID, wt, kept, true, delta, p, reason); err != nil {
		return err
	}
	status := "REJECTED"
	if kept {
		status = "PROVED"
	}
	logf("verdict %s: %s - %s", o.ID, status, reason)
	logf("%s", table)
	return nil
}

// decideVerdict reads the benchstat CSV and decides keep/reject: a statistically significant
// improvement on the proof metric, with no significant regression on the other two metrics. It also
// returns the proof metric's vs-base delta and p-value for the verdict record.
func decideVerdict(csv, label string) (kept bool, delta, p, reason string) {
	delta, p = benchstat.Parse(csv, label)
	switch {
	case delta == "" || delta == "~":
		reason = fmt.Sprintf("no statistically significant change in %s (benchstat: ~)", label)
	case strings.HasPrefix(delta, "-"):
		kept = true
		reason = fmt.Sprintf("significant improvement in %s: %s (p=%s)", label, delta, p)
	default:
		reason = fmt.Sprintf("%s regressed or unchanged: %s", label, delta)
	}
	// regression guard on the other two metrics (significant positive = worse)
	for _, other := range []string{"sec/op", "B/op", "allocs/op"} {
		if other == label {
			continue
		}
		if d, _ := benchstat.Parse(csv, other); strings.HasPrefix(d, "+") {
			kept = false
			reason += fmt.Sprintf("; but %s regressed %s", other, d)
		}
	}
	return kept, delta, p, reason
}

// interleaveBenchstat runs two compiled test binaries alternately (run-by-run) so time-correlated
// machine noise hits both equally, then compares them with benchstat. Shared by Verdict (HEAD vs
// edited) and Regression (base ref vs head ref). Returns the benchstat CSV and the human table, and
// writes baseline.txt/candidate.txt/benchstat.* in runDir.
func interleaveBenchstat(pkgDir, baseBin, headBin, bench string, rounds int, runDir, alpha string) (csv, table string) {
	var baseOut, headOut strings.Builder
	args := []string{"-test.run=^$", "-test.bench=^" + bench + "$", "-test.benchmem", "-test.count=1"}
	for i := 0; i < rounds; i++ {
		b, _, _ := sh.Run(pkgDir, baseBin, args...)
		baseOut.WriteString(b)
		c, _, _ := sh.Run(pkgDir, headBin, args...)
		headOut.WriteString(c)
	}
	baseTxt := filepath.Join(runDir, "baseline.txt")
	headTxt := filepath.Join(runDir, "candidate.txt")
	_ = os.WriteFile(baseTxt, []byte(baseOut.String()), 0o644)
	_ = os.WriteFile(headTxt, []byte(headOut.String()), 0o644)
	csv, _, _ = sh.Run("", "benchstat", "-alpha", alpha, "-format", "csv", baseTxt, headTxt)
	table, _, _ = sh.Run("", "benchstat", "-alpha", alpha, baseTxt, headTxt)
	_ = os.WriteFile(filepath.Join(runDir, "benchstat.csv"), []byte(csv), 0o644)
	_ = os.WriteFile(filepath.Join(runDir, "benchstat.txt"), []byte(table), 0o644)
	return csv, table
}

func writeVerdict(dir, id, wt string, kept, tests bool, delta, p, reason string) error {
	status := "rejected"
	if kept {
		status = "proved"
	}
	bs := ""
	if b, err := os.ReadFile(filepath.Join(dir, "runs", id, "benchstat.txt")); err == nil {
		bs = string(b)
	}
	v := model.Verdict{ID: id, Status: status, Verdict: &model.VerdictDetail{
		Kept: kept, TestsPassed: tests, Delta: delta, PValue: p, Reason: reason, Worktree: wt, Benchstat: bs,
	}}
	return model.WriteJSON(filepath.Join(dir, "runs", id, "verdict.json"), v)
}

// Validate runs Baseline -> apply patch -> Verdict in one shot (the non-LLM path).
func Validate(o Opts, logf func(string, ...any)) error {
	logf = orNoop(logf)
	wt, err := Baseline(o, logf)
	if err != nil {
		return err
	}
	if wt == "" { // baseline wrote a non-worktree verdict (e.g. dependency opt-in needed)
		return nil
	}
	if o.Patch != "" {
		logf("applying patch %s in %s", o.Patch, wt)
		if _, stderr, err := sh.Run(wt, "git", "apply", mustAbs(o.Patch)); err != nil {
			return fmt.Errorf("patch failed: %s", stderr)
		}
	}
	return Verdict(o, logf)
}

// ValidateAll sets up baselines for every hypothesis; the per-hypothesis code change is LLM-applied.
func ValidateAll(o Opts, logf func(string, ...any)) error {
	logf = orNoop(logf)
	hs, err := model.LoadHypotheses(o.Dir)
	if err != nil {
		return err
	}
	for _, h := range hs {
		logf("=== %s ===", h.ID)
		bo := o
		bo.ID = h.ID
		if _, err := Baseline(bo, logf); err != nil {
			logf("  baseline blocked/failed for %s (see runs/%s)", h.ID, h.ID)
			continue
		}
		logf("  (apply the code change in %s/wt/%s, then run: go-perf-agent bench-verdict %s)", o.Dir, h.ID, h.ID)
	}
	logf("note: validate-all only sets up baselines; the per-hypothesis code change is LLM-applied. Use the go-perf-agent skill/agents for the full loop.")
	return nil
}

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

// RegressionOpts configures a base-vs-head regression check.
type RegressionOpts struct {
	Pkg, Bench, Base, Head, ID string
	Dir, Alpha                 string
	BenchCount                 int
}

// Regression reports whether Head got slower than Base for Bench: it builds the benchmark in a
// worktree at each ref, interleaves them, and reads benchstat with an inverted verdict - a
// significant positive delta on any metric in head is a REGRESSION. No code is edited.
func Regression(o RegressionOpts, logf func(string, ...any)) error {
	logf = orNoop(logf)
	runDir := filepath.Join(o.Dir, "runs", o.ID)
	_ = os.MkdirAll(runDir, 0o755)
	arun := mustAbs(runDir)
	rel := benchPkgRel(o.Pkg)

	baseBin, err := buildBenchAt(o.Dir, o.Base, o.ID+"-base", rel, filepath.Join(arun, "base.test"))
	if err != nil {
		return err
	}
	headBin, err := buildBenchAt(o.Dir, o.Head, o.ID+"-head", rel, filepath.Join(arun, "head.test"))
	if err != nil {
		return err
	}

	// both binaries must actually contain the benchmark, else we'd be comparing nothing
	if !benchExists(baseBin, o.Bench) {
		return writeRegression(o, "inconclusive", fmt.Sprintf("benchmark %s not present in base ref %s", o.Bench, o.Base), "", runDir, logf)
	}
	if !benchExists(headBin, o.Bench) {
		return writeRegression(o, "inconclusive", fmt.Sprintf("benchmark %s not present in head ref %s", o.Bench, o.Head), "", runDir, logf)
	}

	logf("interleaved base(%s) vs head(%s): %d rounds of %s", o.Base, o.Head, o.BenchCount, o.Bench)
	// pkgDir for running: use the head worktree's package dir (testdata etc. from head)
	headPkgDir := filepath.Join(o.Dir, "wt", o.ID+"-head", rel)
	csv, table := interleaveBenchstat(headPkgDir, baseBin, headBin, o.Bench, o.BenchCount, runDir, o.Alpha)

	// inverted verdict: any metric significantly WORSE in head (+) is a regression
	var regressions, improvements []string
	for _, m := range []string{"sec/op", "B/op", "allocs/op"} {
		d, p := benchstat.Parse(csv, m)
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
	if err := writeRegression(o, status, reason, table, runDir, logf); err != nil {
		return err
	}
	logf("regression %s: %s - %s", o.ID, strings.ToUpper(status), reason)
	logf("%s", table)
	return nil
}

// buildBenchAt creates a detached worktree at ref and compiles the package's test binary.
func buildBenchAt(dir, ref, wtName, relPkg, outBin string) (string, error) {
	wt := filepath.Join(dir, "wt", wtName)
	if !exists(wt) {
		if _, stderr, err := sh.Run("", "git", "worktree", "add", "-q", "--detach", wt, ref); err != nil {
			return "", fmt.Errorf("git worktree add %s at %s failed: %s", wt, ref, stderr)
		}
	}
	pkgDir := filepath.Join(wt, relPkg)
	if _, stderr, err := sh.Run(pkgDir, "go", "test", "-c", "-o", outBin, "."); err != nil {
		return "", fmt.Errorf("compile bench at %s failed: %s", ref, stderr)
	}
	return outBin, nil
}

func benchExists(bin, bench string) bool {
	out, _, _ := sh.Run("", bin, "-test.run=^$", "-test.bench=^"+bench+"$", "-test.count=1")
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, bench) {
			return true
		}
	}
	return false
}

func writeRegression(o RegressionOpts, status, reason, table, runDir string, logf func(string, ...any)) error {
	v := RegressionVerdict{
		ID: o.ID, Pkg: o.Pkg, Bench: o.Bench, BaseRef: o.Base, HeadRef: o.Head,
		Status: status, Reason: reason, Benchstat: table,
	}
	if status == "inconclusive" {
		logf("regression %s: INCONCLUSIVE - %s", o.ID, reason)
	}
	return model.WriteJSON(filepath.Join(runDir, "regression.json"), v)
}

// needsDependencyOptIn reports whether a hypothesis edits a dependency (vendored OSS / generated)
// whose in-tree path the current scope does not include yet, so it must not be auto-validated until
// the user opts in by scoping to that path.
func needsDependencyOptIn(h *model.Hypothesis, sc *hotspot.Scope) bool {
	return h.Dependency != nil && h.Dependency.Path != "" && !hotspot.InScope(h.Dependency.Path, sc)
}

// benchPkgRel normalizes a benchmark pkg to a worktree-relative dir to cd into: a trailing "..." is
// a valid `go test` wildcard but not a real directory ("./..." -> module root "").
func benchPkgRel(pkg string) string {
	p := strings.TrimPrefix(pkg, "./")
	return strings.TrimRight(strings.TrimSuffix(p, "..."), "/")
}

func mustAbs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func orNoop(logf func(string, ...any)) func(string, ...any) {
	if logf == nil {
		return func(string, ...any) {}
	}
	return logf
}

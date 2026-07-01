// Package gate is the benchmarking engine: it sets up per-hypothesis worktrees, compiles pristine
// baseline binaries, runs interleaved A/B benchmarks, and decides PROVED / REJECTED / NEED_MORE_DATA
// with benchstat. It also runs the base-vs-head regression check. Keep/reject comes from benchstat
// alone, so a win is measured, not asserted. logf takes progress (caller routes it - pass nil for silent).
package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"go-perf-agent/internal/helper"
	"go-perf-agent/internal/hotspot"
	"go-perf-agent/internal/model"
)

// Opts configures a single-hypothesis run. Dir is the .go-perf-agent working dir.
type Opts struct {
	ID             string
	Dir            string
	BenchCount     int
	Alpha          string
	Patch          string  // validate only: git patch applied in the worktree before the verdict
	MinImprovement float64 // effect-size floor (%) the proof metric must clear to count as a win
	RegressionTol  float64 // tolerance (%) a non-proof metric may regress within before killing the win
}

// versionPinNote surfaces the deployed ref the telemetry came from against the worktree HEAD being
// measured, so measuring code the prod numbers did not come from is visible. "" when unknown.
func versionPinNote(dir, head string) string {
	dv, err := os.ReadFile(filepath.Join(dir, "deployed_version"))
	if err != nil {
		return ""
	}
	return fmt.Sprintf("version pin: baseline off HEAD %s, telemetry from deployed version %s - validate against the matching ref if they differ", head, strings.TrimSpace(string(dv)))
}

// Baseline creates the worktree and compiles the PRISTINE baseline test binary now, before any
// edit. A compiled binary is frozen against later source edits, so Verdict can interleave it
// against the candidate to cancel time-correlated machine noise. Returns the worktree path ("" when
// it wrote a non-worktree verdict, e.g. dependency opt-in needed).
func Baseline(o Opts, logf func(string, ...any)) (string, error) {
	logf = helper.OrNoop(logf)
	hyp, err := model.GetHypothesis(o.Dir, o.ID)
	if err != nil {
		return "", err
	}
	runDir := filepath.Join(o.Dir, "runs", o.ID)
	wt := filepath.Join(o.Dir, "wt", o.ID)
	_ = os.MkdirAll(runDir, 0o755)
	arun := helper.MustAbs(runDir)

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

	if helper.Exists(wt) {
		logf("worktree exists, reusing %s", wt)
	} else {
		logf("creating worktree %s off HEAD", wt)
		if _, stderr, err := helper.Run("", "git", "worktree", "add", "-q", "--detach", wt, "HEAD"); err != nil {
			return "", fmt.Errorf("git worktree add failed: %s", stderr)
		}
	}
	head, _, _ := helper.Run(wt, "git", "rev-parse", "--short", "HEAD")
	if note := versionPinNote(o.Dir, strings.TrimSpace(head)); note != "" {
		logf("%s", note)
	}

	pkgDir := filepath.Join(wt, benchPkgRel(hyp.Benchmark.Pkg))

	// resolve the benchmark. Adopt an authored one from the worktree so a re-run after authoring
	// proceeds, instead of dead-ending on needs_authoring and forcing a hand-edit of hypotheses.json.
	if hyp.Benchmark.NeedsAuthoring || hyp.Benchmark.Name == "" {
		benches := detectBenchmarks(pkgDir)
		name := hyp.Benchmark.Name
		switch {
		case name != "" && slices.Contains(benches, name):
			// authored under the named benchmark - use it
		case name == "" && len(benches) == 1:
			name = benches[0]
		case len(benches) == 0:
			logf("NEEDS_BENCHMARK: %s needs a benchmark authored in the worktree package %s (%s), then re-run bench baseline.", o.ID, pkgDir, hyp.Benchmark.Pkg)
			_ = model.WriteJSON(filepath.Join(runDir, "verdict.json"), model.Verdict{
				ID: o.ID, Status: "need_more_data", Reason: "benchmark needs authoring",
				Verdict: &model.VerdictDetail{Worktree: wt},
			})
			return wt, nil
		default:
			logf("NEEDS_BENCHMARK: %s - the worktree package %s has several benchmarks %v. Set benchmark.name in hypotheses.json to the one that exercises the change, then re-run.", o.ID, pkgDir, benches)
			_ = model.WriteJSON(filepath.Join(runDir, "verdict.json"), model.Verdict{
				ID: o.ID, Status: "need_more_data", Reason: "several benchmarks present - name the target in benchmark.name",
				Verdict: &model.VerdictDetail{Worktree: wt},
			})
			return wt, nil
		}
		// persist the resolved benchmark so Verdict reuses it without a hand-edit
		if err := model.SetBenchmarkName(o.Dir, o.ID, name); err != nil {
			return "", err
		}
		hyp.Benchmark.Name = name
		logf("adopted authored benchmark %s for %s", name, o.ID)
	}

	logf("compiling baseline binary for %s (%s)", hyp.Benchmark.Name, hyp.Benchmark.Pkg)
	if _, stderr, err := helper.Run(pkgDir, "go", "test", "-c", "-o", filepath.Join(arun, "baseline.test"), "."); err != nil {
		return "", fmt.Errorf("baseline compile failed for %s: %s", o.ID, stderr)
	}
	// snapshot the test files now (after any benchmark authoring) so Verdict can detect a candidate
	// that later edits the benchmark/test to game the result.
	_ = os.WriteFile(filepath.Join(runDir, "baseline-tests.sha"), []byte(testFilesHash(pkgDir)), 0o644)
	if len(hyp.Evidence) == 0 || string(hyp.Evidence) == "null" {
		logf("warning: hypothesis %s has no evidence - it should cite a real signal (profile/trace)", o.ID)
	}
	// smoke-run once so the user sees the starting numbers
	if out, _, err := helper.Run(pkgDir, filepath.Join(arun, "baseline.test"),
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

var benchFuncRe = regexp.MustCompile(`(?m)^func (Benchmark\w+)\(`)

// detectBenchmarks returns the Benchmark funcs declared in the package's *_test.go files, so bench
// baseline can adopt an authored benchmark instead of dead-ending on needs_authoring.
func detectBenchmarks(pkgDir string) []string {
	var out []string
	files, _ := filepath.Glob(filepath.Join(pkgDir, "*_test.go"))
	for _, f := range files {
		b, _ := os.ReadFile(f)
		for _, m := range benchFuncRe.FindAllStringSubmatch(string(b), -1) {
			out = append(out, m[1])
		}
	}
	return out
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
	out, _, _ := helper.Run(wt, "git", "status", "--porcelain")
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
	logf = helper.OrNoop(logf)
	hyp, err := model.GetHypothesis(o.Dir, o.ID)
	if err != nil {
		return err
	}
	wt := filepath.Join(o.Dir, "wt", o.ID)
	runDir := filepath.Join(o.Dir, "runs", o.ID)
	arun := helper.MustAbs(runDir)
	pkgDir := filepath.Join(wt, benchPkgRel(hyp.Benchmark.Pkg))
	baselineBin := filepath.Join(arun, "baseline.test")
	if !helper.Exists(wt) {
		return fmt.Errorf("no worktree for %s; run bench baseline first", o.ID)
	}
	if !helper.Exists(baselineBin) {
		return fmt.Errorf("no baseline binary for %s; run bench baseline first", o.ID)
	}

	// serialize measurement: a concurrent bench would contend for CPU and defeat the interleaving
	release, err := acquireBenchLock(o.Dir)
	if err != nil {
		return err
	}
	defer release()

	// structural gate: refuse a dishonest change (gamed ruler / out-of-scope edit) before measuring
	if reason := structuralGate(o.Dir, wt, pkgDir, runDir); reason != "" {
		logf("  REJECTED (structural): %s", reason)
		return writeVerdict(o.Dir, o.ID, wt, false, false, "", "", reason)
	}

	// correctness gate first - a faster-but-wrong change is a rejection, not a win
	logf("tests: go test %s -count=1 (in worktree)", hyp.Benchmark.Pkg)
	if tout, terr, err := helper.Run(wt, "go", "test", hyp.Benchmark.Pkg, "-count=1"); err != nil {
		logf("  FAIL - correctness broken, rejecting")
		_ = os.WriteFile(filepath.Join(runDir, "tests.txt"), []byte(tout+terr), 0o644)
		return writeVerdict(o.Dir, o.ID, wt, false, false, "", "", "tests failed after change")
	}

	logf("compiling candidate binary")
	candidateBin := filepath.Join(arun, "candidate.test")
	if _, stderr, err := helper.Run(pkgDir, "go", "test", "-c", "-o", candidateBin, "."); err != nil {
		return fmt.Errorf("candidate compile failed for %s: %s", o.ID, stderr)
	}

	logf("interleaved benchmarking: %d rounds of %s", o.BenchCount, hyp.Benchmark.Name)
	csv, table, err := interleaveBenchstat(pkgDir, baselineBin, candidateBin, hyp.Benchmark.Name, o.BenchCount, runDir, o.Alpha)
	if err != nil {
		return err
	}

	label := proofLabel(hyp.Metric)
	if label == "" {
		return fmt.Errorf("unknown metric %s", hyp.Metric)
	}
	if n := benchstatRowCount(csv, label); n > 1 {
		logf("warning: %s has %d benchmark rows (b.Run subtests); the gate reads only the first - name a specific subtest in the hypothesis benchmark to measure the one you mean", hyp.Benchmark.Name, n)
	}
	kept, delta, p, reason := decideVerdict(csv, label, o.MinImprovement, o.RegressionTol)

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

// decideVerdict reads the benchstat CSV and decides keep/reject. Significance alone is not enough:
// the proof-metric improvement must clear minImprove (an effect-size floor, so noise-level "wins" on
// a fast bench do not count), and a significant regression on another metric only kills the win if
// it exceeds regressTol (so a real win is not lost to a fractional blip). Both are percent.
func decideVerdict(csv, label string, minImprove, regressTol float64) (kept bool, delta, p, reason string) {
	delta, p = parseBenchstat(csv, label)
	switch {
	case delta == "" || delta == "~":
		reason = fmt.Sprintf("no statistically significant change in %s (benchstat: ~)", label)
	case strings.HasPrefix(delta, "-"):
		if imp := -pctValue(delta); imp >= minImprove {
			kept = true
			reason = fmt.Sprintf("significant improvement in %s: %s (p=%s)", label, delta, p)
		} else {
			reason = fmt.Sprintf("improvement in %s too small to ship: %s (< %.1f%% floor)", label, delta, minImprove)
		}
	default:
		reason = fmt.Sprintf("%s regressed or unchanged: %s", label, delta)
	}
	// regression guard: a significant regression on another metric kills the win only beyond tolerance
	for _, other := range []string{"sec/op", "B/op", "allocs/op"} {
		if other == label {
			continue
		}
		if d, _ := parseBenchstat(csv, other); strings.HasPrefix(d, "+") && pctValue(d) > regressTol {
			kept = false
			reason += fmt.Sprintf("; but %s regressed %s (> %.1f%% tolerance)", other, d, regressTol)
		}
	}
	return kept, delta, p, reason
}

// pctValue parses a benchstat "vs base" cell ("-19.04%", "+0.5%", "~") to its signed percent, or 0
// when it is not a number (e.g. "~").
func pctValue(delta string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(delta), "%"), 64)
	return v
}

// interleaveBenchstat runs two compiled test binaries alternately (run-by-run) so time-correlated
// machine noise hits both equally, then compares them with benchstat. Shared by Verdict (HEAD vs
// edited) and Regression (base ref vs head ref). Returns the benchstat CSV and the human table, and
// writes baseline.txt/candidate.txt/benchstat.* in runDir.
func interleaveBenchstat(pkgDir, baseBin, headBin, bench string, rounds int, runDir, alpha string) (csv, table string, err error) {
	var baseOut, headOut strings.Builder
	args := []string{"-test.run=^$", "-test.bench=^" + bench + "$", "-test.benchmem", "-test.count=1"}
	for i := 0; i < rounds; i++ {
		b, _, _ := helper.Run(pkgDir, baseBin, args...)
		baseOut.WriteString(b)
		c, _, _ := helper.Run(pkgDir, headBin, args...)
		headOut.WriteString(c)
	}
	baseTxt := filepath.Join(runDir, "baseline.txt")
	headTxt := filepath.Join(runDir, "candidate.txt")
	_ = os.WriteFile(baseTxt, []byte(baseOut.String()), 0o644)
	_ = os.WriteFile(headTxt, []byte(headOut.String()), 0o644)
	// a benchstat failure must NOT look like "no change" - surface it, else the gate silently rejects.
	var stderr string
	if csv, stderr, err = helper.Run("", "benchstat", "-alpha", alpha, "-format", "csv", baseTxt, headTxt); err != nil {
		return "", "", fmt.Errorf("benchstat failed (on PATH? valid benchmark output?): %v: %s", err, stderr)
	}
	table, _, _ = helper.Run("", "benchstat", "-alpha", alpha, baseTxt, headTxt)
	_ = os.WriteFile(filepath.Join(runDir, "benchstat.csv"), []byte(csv), 0o644)
	_ = os.WriteFile(filepath.Join(runDir, "benchstat.txt"), []byte(table), 0o644)
	return csv, table, nil
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
	logf = helper.OrNoop(logf)
	wt, err := Baseline(o, logf)
	if err != nil {
		return err
	}
	if wt == "" { // baseline wrote a non-worktree verdict (e.g. dependency opt-in needed)
		return nil
	}
	if o.Patch != "" {
		logf("applying patch %s in %s", o.Patch, wt)
		if _, stderr, err := helper.Run(wt, "git", "apply", helper.MustAbs(o.Patch)); err != nil {
			return fmt.Errorf("patch failed: %s", stderr)
		}
	}
	return Verdict(o, logf)
}

// ValidateAll sets up baselines for every hypothesis - the per-hypothesis code change is LLM-applied.
func ValidateAll(o Opts, logf func(string, ...any)) error {
	logf = helper.OrNoop(logf)
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
		logf("  (apply the code change in %s/wt/%s, then run: go-perf-agent bench verdict %s)", o.Dir, h.ID, h.ID)
	}
	logf("note: validate --all only sets up baselines; the per-hypothesis code change is LLM-applied. Use the go-perf-agent skill/agents for the full loop.")
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
	RegressionTol              float64 // a positive delta below this (%) is noise, not a regression
}

// Regression reports whether Head got slower than Base for Bench: it builds the benchmark in a
// worktree at each ref, interleaves them, and reads benchstat with an inverted verdict - a
// significant positive delta on any metric in head is a REGRESSION. No code is edited.
func Regression(o RegressionOpts, logf func(string, ...any)) error {
	logf = helper.OrNoop(logf)
	runDir := filepath.Join(o.Dir, "runs", o.ID)
	_ = os.MkdirAll(runDir, 0o755)
	arun := helper.MustAbs(runDir)
	rel := benchPkgRel(o.Pkg)

	// serialize measurement: a concurrent bench would contend for CPU and defeat the interleaving
	release, err := acquireBenchLock(o.Dir)
	if err != nil {
		return err
	}
	defer release()

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
	csv, table, err := interleaveBenchstat(headPkgDir, baseBin, headBin, o.Bench, o.BenchCount, runDir, o.Alpha)
	if err != nil {
		return err
	}

	// inverted verdict: a significant positive delta beyond tolerance is a REGRESSION
	var regressions, improvements []string
	for _, m := range []string{"sec/op", "B/op", "allocs/op"} {
		d, p := parseBenchstat(csv, m)
		switch {
		case strings.HasPrefix(d, "+") && pctValue(d) > o.RegressionTol:
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
	if !helper.Exists(wt) {
		if _, stderr, err := helper.Run("", "git", "worktree", "add", "-q", "--detach", wt, ref); err != nil {
			return "", fmt.Errorf("git worktree add %s at %s failed: %s", wt, ref, stderr)
		}
	}
	pkgDir := filepath.Join(wt, relPkg)
	if _, stderr, err := helper.Run(pkgDir, "go", "test", "-c", "-o", outBin, "."); err != nil {
		return "", fmt.Errorf("compile bench at %s failed: %s", ref, stderr)
	}
	return outBin, nil
}

func benchExists(bin, bench string) bool {
	out, _, _ := helper.Run("", bin, "-test.run=^$", "-test.bench=^"+bench+"$", "-test.count=1")
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

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type benchBaselineCmd struct {
	ID string `arg:"" help:"hypothesis id (from .go-perf-agent/hypotheses.json)"`
}

func (c *benchBaselineCmd) Run() error { _, err := runBenchBaseline(c.ID); return err }

// runBenchBaseline creates the worktree and compiles the PRISTINE baseline test binary now,
// before any edit. A compiled binary is frozen against later source edits, so bench-verdict can
// interleave it against the candidate to cancel time-correlated machine noise.
func runBenchBaseline(id string) (string, error) {
	hyp, err := getHypothesis(id)
	if err != nil {
		return "", err
	}
	ensureDirs()
	runDir := filepath.Join(gpaDir, "runs", id)
	wt := filepath.Join(gpaDir, "wt", id)
	_ = os.MkdirAll(runDir, 0o755)
	arun := mustAbs(runDir)

	if fileExists(wt) {
		info("worktree exists, reusing %s", wt)
	} else {
		info("creating worktree %s off HEAD", wt)
		if _, stderr, err := run("", "git", "worktree", "add", "-q", "--detach", wt, "HEAD"); err != nil {
			return "", fmt.Errorf("git worktree add failed: %s", stderr)
		}
	}

	if hyp.Benchmark.NeedsAuthoring || hyp.Benchmark.Name == "" {
		// not a final verdict - signal the validation agent to author a benchmark, then re-run.
		fmt.Fprintf(os.Stderr, "NEEDS_BENCHMARK: hypothesis %s needs a benchmark authored in %s (%s). The validation agent writes it, then re-runs bench-baseline.\n", id, wt, hyp.Benchmark.Pkg)
		_ = writeJSON(filepath.Join(runDir, "verdict.json"), Verdict{
			ID: id, Status: "need_more_data", Reason: "benchmark needs authoring",
			Verdict: &VerdictDetail{Worktree: wt},
		})
		return wt, nil
	}

	pkgDir := filepath.Join(wt, benchPkgRel(hyp.Benchmark.Pkg))
	info("compiling baseline binary for %s (%s)", hyp.Benchmark.Name, hyp.Benchmark.Pkg)
	if _, stderr, err := run(pkgDir, "go", "test", "-c", "-o", filepath.Join(arun, "baseline.test"), "."); err != nil {
		return "", fmt.Errorf("baseline compile failed for %s: %s", id, stderr)
	}
	// snapshot the test files now (after any benchmark authoring) so bench-verdict can detect a
	// candidate that later edits the benchmark/test to game the result.
	_ = os.WriteFile(filepath.Join(runDir, "baseline-tests.sha"), []byte(testFilesHash(pkgDir)), 0o644)
	if len(hyp.Evidence) == 0 || string(hyp.Evidence) == "null" {
		info("warning: hypothesis %s has no evidence - it should cite a real signal (profile/trace)", id)
	}
	// smoke-run once so the user sees the starting numbers
	if out, _, err := run(pkgDir, filepath.Join(arun, "baseline.test"),
		"-test.run=^$", "-test.bench=^"+hyp.Benchmark.Name+"$", "-test.benchmem", "-test.count=1"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, hyp.Benchmark.Name) {
				fmt.Fprintln(os.Stderr, line)
			}
		}
	}
	fmt.Println(wt)
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

// structuralGate enforces, in code (not prompt), that the candidate change is honest: it must
// not modify the benchmark/test being judged, and must stay inside scope. Returns a reject
// reason, or "" if the change is structurally allowed.
func structuralGate(id, wt, pkgDir, runDir string) string {
	if sha, err := os.ReadFile(filepath.Join(runDir, "baseline-tests.sha")); err == nil {
		if testFilesHash(pkgDir) != strings.TrimSpace(string(sha)) {
			return "candidate modified the benchmark/test files - cannot change the ruler"
		}
	}
	sc := loadScope()
	if sc == nil {
		return ""
	}
	out, _, _ := run(wt, "git", "status", "--porcelain")
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
		if !inScope(filepath.Dir(path), sc) {
			return "candidate edited out-of-scope file: " + path
		}
	}
	return ""
}

type benchVerdictCmd struct {
	ID string `arg:"" help:"hypothesis id"`
}

func (c *benchVerdictCmd) Run() error { return runBenchVerdict(c.ID) }

// runBenchVerdict is the numeric gate. Correctness tests, then INTERLEAVED A/B benchmarks
// (baseline and candidate binaries alternated run-by-run so drift hits both equally), then
// benchstat. PROVED iff tests pass AND significant improvement on the proof metric AND no other
// metric regresses.
func runBenchVerdict(id string) error {
	hyp, err := getHypothesis(id)
	if err != nil {
		return err
	}
	wt := filepath.Join(gpaDir, "wt", id)
	runDir := filepath.Join(gpaDir, "runs", id)
	arun := mustAbs(runDir)
	pkgDir := filepath.Join(wt, benchPkgRel(hyp.Benchmark.Pkg))
	baselineBin := filepath.Join(arun, "baseline.test")
	if !fileExists(wt) {
		return fmt.Errorf("no worktree for %s; run bench-baseline first", id)
	}
	if !fileExists(baselineBin) {
		return fmt.Errorf("no baseline binary for %s; run bench-baseline first", id)
	}

	// structural gate: refuse a dishonest change (gamed ruler / out-of-scope edit) before measuring
	if reason := structuralGate(id, wt, pkgDir, runDir); reason != "" {
		info("  REJECTED (structural): %s", reason)
		return writeVerdict(id, wt, false, false, "", "", "", reason)
	}

	// correctness gate first - a faster-but-wrong change is a rejection, not a win
	info("tests: go test %s -count=1 (in worktree)", hyp.Benchmark.Pkg)
	if tout, terr, err := run(wt, "go", "test", hyp.Benchmark.Pkg, "-count=1"); err != nil {
		info("  FAIL - correctness broken, rejecting")
		_ = os.WriteFile(filepath.Join(runDir, "tests.txt"), []byte(tout+terr), 0o644)
		return writeVerdict(id, wt, false, false, "", "", "", "tests failed after change")
	}

	info("compiling candidate binary")
	candidateBin := filepath.Join(arun, "candidate.test")
	if _, stderr, err := run(pkgDir, "go", "test", "-c", "-o", candidateBin, "."); err != nil {
		return fmt.Errorf("candidate compile failed for %s: %s", id, stderr)
	}

	info("interleaved benchmarking: %d rounds of %s", benchCount, hyp.Benchmark.Name)
	csv, table := interleaveBenchstat(pkgDir, baselineBin, candidateBin, hyp.Benchmark.Name, benchCount, runDir)
	bsFile := filepath.Join(runDir, "benchstat.txt")

	label := proofLabel(hyp.Metric)
	if label == "" {
		return fmt.Errorf("unknown metric %s", hyp.Metric)
	}
	proofDelta, proofP := parseBenchstat(csv, label)

	kept := false
	var reason string
	switch {
	case proofDelta == "" || proofDelta == "~":
		reason = fmt.Sprintf("no statistically significant change in %s (benchstat: ~)", label)
	case strings.HasPrefix(proofDelta, "-"):
		kept = true
		reason = fmt.Sprintf("significant improvement in %s: %s (p=%s)", label, proofDelta, proofP)
	default:
		reason = fmt.Sprintf("%s regressed or unchanged: %s", label, proofDelta)
	}
	// regression guard on the other two metrics (significant positive = worse)
	for _, other := range []string{"sec/op", "B/op", "allocs/op"} {
		if other == label {
			continue
		}
		if d, _ := parseBenchstat(csv, other); strings.HasPrefix(d, "+") {
			kept = false
			reason += fmt.Sprintf("; but %s regressed %s", other, d)
		}
	}

	if err := writeVerdict(id, wt, kept, true, proofDelta, proofP, bsFile, reason); err != nil {
		return err
	}
	status := "REJECTED"
	if kept {
		status = "PROVED"
	}
	info("verdict %s: %s - %s", id, status, reason)
	fmt.Fprint(os.Stderr, table)
	return nil
}

func proofLabel(metric string) string {
	switch metric {
	case "ns_op":
		return "sec/op"
	case "B_op":
		return "B/op"
	case "allocs_op":
		return "allocs/op"
	}
	return ""
}

// interleaveBenchstat runs two compiled test binaries alternately (run-by-run) so
// time-correlated machine noise hits both equally, then compares them with benchstat. Shared by
// bench-verdict (HEAD-pristine vs edited) and bench-regression (base ref vs head ref). Returns
// the benchstat CSV and the human table, and writes baseline.txt/candidate.txt/benchstat.* in runDir.
func interleaveBenchstat(pkgDir, baseBin, headBin, bench string, rounds int, runDir string) (csv, table string) {
	var baseOut, headOut strings.Builder
	args := []string{"-test.run=^$", "-test.bench=^" + bench + "$", "-test.benchmem", "-test.count=1"}
	for i := 0; i < rounds; i++ {
		b, _, _ := run(pkgDir, baseBin, args...)
		baseOut.WriteString(b)
		c, _, _ := run(pkgDir, headBin, args...)
		headOut.WriteString(c)
	}
	baseTxt := filepath.Join(runDir, "baseline.txt")
	headTxt := filepath.Join(runDir, "candidate.txt")
	_ = os.WriteFile(baseTxt, []byte(baseOut.String()), 0o644)
	_ = os.WriteFile(headTxt, []byte(headOut.String()), 0o644)
	csv, _, _ = run("", "benchstat", "-alpha", alpha, "-format", "csv", baseTxt, headTxt)
	table, _, _ = run("", "benchstat", "-alpha", alpha, baseTxt, headTxt)
	_ = os.WriteFile(filepath.Join(runDir, "benchstat.csv"), []byte(csv), 0o644)
	_ = os.WriteFile(filepath.Join(runDir, "benchstat.txt"), []byte(table), 0o644)
	return csv, table
}

// parseBenchstat reads benchstat `-format csv` and returns ("vs base", p) for one metric.
// A metric section starts with a header row `,<label>,CI,<label>,CI,vs base,P`; the next data
// row (col 0 non-empty, not "geomean") carries vs base at col len-2 and "p=.. n=.." at col len-1.
// vs base is "~" when not significant, else a signed percentage like "-19.04%".
func parseBenchstat(csv, label string) (vsBase, p string) {
	inSection := false
	for _, line := range strings.Split(csv, "\n") {
		cols := strings.Split(line, ",")
		if strings.Contains(line, ","+label+",CI") {
			inSection = true
			continue
		}
		if strings.TrimSpace(line) == "" {
			inSection = false
			continue
		}
		if inSection && len(cols) >= 7 && cols[0] != "" && cols[0] != "geomean" {
			vsBase = cols[len(cols)-2]
			p = strings.TrimPrefix(cols[len(cols)-1], "p=")
			if i := strings.Index(p, " "); i >= 0 {
				p = p[:i]
			}
			return vsBase, p
		}
	}
	return "", ""
}

func writeVerdict(id, wt string, kept, tests bool, delta, p, bsFile, reason string) error {
	status := "rejected"
	if kept {
		status = "proved"
	}
	bs := ""
	if bsFile != "" {
		if b, err := os.ReadFile(bsFile); err == nil {
			bs = string(b)
		}
	}
	v := Verdict{ID: id, Status: status, Verdict: &VerdictDetail{
		Kept: kept, TestsPassed: tests, Delta: delta, PValue: p, Reason: reason, Worktree: wt, Benchstat: bs,
	}}
	return writeJSON(filepath.Join(gpaDir, "runs", id, "verdict.json"), v)
}

// validate: baseline -> apply patch -> verdict, in one shot (non-LLM path).
type validateCmd struct {
	ID    string `arg:"" help:"hypothesis id"`
	Patch string `help:"git patch to apply in the worktree before the verdict"`
}

func (c *validateCmd) Run() error {
	wt, err := runBenchBaseline(c.ID)
	if err != nil {
		return err
	}
	if c.Patch != "" {
		info("applying patch %s in %s", c.Patch, wt)
		if _, stderr, err := run(wt, "git", "apply", mustAbs(c.Patch)); err != nil {
			return fmt.Errorf("patch failed: %s", stderr)
		}
	}
	return runBenchVerdict(c.ID)
}

type validateAllCmd struct{}

func (c *validateAllCmd) Run() error {
	hs, err := loadHypotheses()
	if err != nil {
		return err
	}
	for _, h := range hs {
		info("=== %s ===", h.ID)
		if _, err := runBenchBaseline(h.ID); err != nil {
			info("  baseline blocked/failed for %s (see runs/%s)", h.ID, h.ID)
			continue
		}
		info("  (apply the code change in %s/wt/%s, then run: go-perf-agent bench-verdict %s)", gpaDir, h.ID, h.ID)
	}
	info("note: validate-all only sets up baselines; the per-hypothesis code change is LLM-applied. Use the go-perf-agent skill/agents for the full loop.")
	return nil
}

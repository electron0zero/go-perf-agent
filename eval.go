package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// eval runs the golden scenarios and checks the deterministic engine produces the expected
// verdict for each known situation. Each scenario runs --runs times so flakiness shows up (a
// capability that only passes sometimes is luck, not reliability). This is how we catch our own
// regressions when the gate / structural checks / commands change.
type evalCmd struct {
	Dir        string `default:"eval/scenarios" help:"scenarios directory"`
	Runs       int    `default:"3" help:"runs per scenario (to catch flakiness)"`
	Only       string `help:"only run scenarios whose name contains this"`
	BenchCount int    `default:"10" help:"GPA_BENCH_COUNT for each scenario run"`
}

type expectedSpec struct {
	Type         string          `json:"type"`          // verdict | regression
	ExpectStatus string          `json:"expect_status"` // proved|rejected|need_more_data|regression|clean|inconclusive
	Hypothesis   json.RawMessage `json:"hypothesis"`
	Pkg          string          `json:"pkg"`
	Bench        string          `json:"bench"`
	ScopeInclude []string        `json:"scope_include"`
}

type scenarioResult struct {
	name     string
	expected string
	got      []string // per run
	verdict  string   // PASS | FAIL | FLAKY | ERROR
	avgMS    int64
}

func (c *evalCmd) Run() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		return fmt.Errorf("read scenarios %s: %w", c.Dir, err)
	}

	var results []scenarioResult
	for _, e := range entries {
		if !e.IsDir() || (c.Only != "" && !strings.Contains(e.Name(), c.Only)) {
			continue
		}
		dir := filepath.Join(c.Dir, e.Name())
		specRaw, err := os.ReadFile(filepath.Join(dir, "expected.json"))
		if err != nil {
			continue // not a scenario
		}
		var spec expectedSpec
		if err := json.Unmarshal(specRaw, &spec); err != nil {
			results = append(results, scenarioResult{name: e.Name(), verdict: "ERROR", expected: "parse expected.json: " + err.Error()})
			continue
		}
		res := scenarioResult{name: e.Name(), expected: spec.ExpectStatus}
		var total time.Duration
		for r := 0; r < c.Runs; r++ {
			t0 := time.Now()
			got, err := runScenario(self, dir, spec, c.BenchCount)
			total += time.Since(t0)
			if err != nil {
				got = "error:" + err.Error()
			}
			res.got = append(res.got, got)
		}
		res.avgMS = total.Milliseconds() / int64(max(c.Runs, 1))
		res.verdict = grade(spec.ExpectStatus, res.got)
		info("%-22s expected=%-10s got=%v -> %s", res.name, res.expected, res.got, res.verdict)
		results = append(results, res)
	}

	return reportEval(results)
}

// runScenario sets up a throwaway repo from the scenario, runs the engine via the binary itself,
// and returns the resulting status. Each scenario is fully isolated in its own temp dir.
func runScenario(self, dir string, spec expectedSpec, benchCount int) (string, error) {
	tmp, err := os.MkdirTemp("", "gpa-eval-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	env := []string{"GPA_BENCH_COUNT=" + strconv.Itoa(benchCount)}

	if err := copyDir(filepath.Join(dir, "base"), tmp); err != nil {
		return "", err
	}
	if err := gitInitCommit(tmp, "base"); err != nil {
		return "", err
	}

	if spec.Type == "regression" {
		base, _, _ := run(tmp, "git", "rev-parse", "HEAD")
		base = strings.TrimSpace(base)
		if err := copyDir(filepath.Join(dir, "candidate"), tmp); err != nil {
			return "", err
		}
		if err := gitInitCommit(tmp, "head"); err != nil {
			return "", err
		}
		if _, e := runSelf(self, tmp, env, "bench-regression", "--pkg", spec.Pkg, "--bench", spec.Bench, "--base", base); e != nil {
			return "", e
		}
		return readStatus(filepath.Join(tmp, gpaDir, "runs", "regression", "regression.json"))
	}

	// verdict / structural
	id := hypothesisID(spec.Hypothesis)
	_ = os.MkdirAll(filepath.Join(tmp, gpaDir), 0o755)
	if err := os.WriteFile(filepath.Join(tmp, gpaDir, "hypotheses.json"), []byte("["+string(spec.Hypothesis)+"]"), 0o644); err != nil {
		return "", err
	}
	if len(spec.ScopeInclude) > 0 {
		if _, e := runSelf(self, tmp, env, "scope", "--include", strings.Join(spec.ScopeInclude, ",")); e != nil {
			return "", e
		}
	}
	if _, e := runSelf(self, tmp, env, "bench-baseline", id); e != nil {
		return "", e
	}
	// apply the candidate change INTO THE WORKTREE (where bench-verdict judges), exactly like the
	// validation agent edits .go-perf-agent/wt/<id>/ - not the repo root.
	if fileExists(filepath.Join(dir, "candidate")) {
		if err := copyDir(filepath.Join(dir, "candidate"), filepath.Join(tmp, gpaDir, "wt", id)); err != nil {
			return "", err
		}
	}
	if _, e := runSelf(self, tmp, env, "bench-verdict", id); e != nil {
		return "", e
	}
	return readStatus(filepath.Join(tmp, gpaDir, "runs", id, "verdict.json"))
}

func grade(expected string, got []string) string {
	allMatch, anyMatch, vary := true, false, false
	for i, g := range got {
		if g != expected {
			allMatch = false
		} else {
			anyMatch = true
		}
		if i > 0 && g != got[0] {
			vary = true
		}
	}
	switch {
	case allMatch:
		return "PASS"
	case vary && anyMatch:
		return "FLAKY"
	default:
		return "FAIL"
	}
}

func reportEval(results []scenarioResult) error {
	pass, fail, flaky := 0, 0, 0
	fmt.Println("\n=== eval ===")
	fmt.Printf("%-22s %-12s %-7s %s\n", "scenario", "expected", "ms", "result")
	for _, r := range results {
		switch r.verdict {
		case "PASS":
			pass++
		case "FLAKY":
			flaky++
		default:
			fail++
		}
		fmt.Printf("%-22s %-12s %-7d %s %v\n", r.name, r.expected, r.avgMS, r.verdict, r.got)
	}
	fmt.Printf("\n%d passed, %d flaky, %d failed (of %d)\n", pass, flaky, fail, len(results))
	if fail > 0 || flaky > 0 {
		return fmt.Errorf("eval not green: %d failed, %d flaky", fail, flaky)
	}
	return nil
}

// ---- helpers ----------------------------------------------------------------------------

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

func gitInitCommit(dir, msg string) error {
	if !fileExists(filepath.Join(dir, ".git")) {
		if _, s, err := run(dir, "git", "init", "-q"); err != nil {
			return fmt.Errorf("git init: %s", s)
		}
		_, _, _ = run(dir, "git", "config", "user.email", "eval@local")
		_, _, _ = run(dir, "git", "config", "user.name", "eval")
	}
	if _, s, err := run(dir, "git", "add", "-A"); err != nil {
		return fmt.Errorf("git add: %s", s)
	}
	if _, s, err := run(dir, "git", "commit", "-q", "-m", msg); err != nil {
		return fmt.Errorf("git commit: %s", s)
	}
	return nil
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

func readStatus(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("no verdict at %s", path)
	}
	var v struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return "", err
	}
	return v.Status, nil
}

func hypothesisID(raw json.RawMessage) string {
	var h struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &h)
	if h.ID == "" {
		return "h"
	}
	return h.ID
}

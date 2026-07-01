package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// smokeCmd proves the WHOLE loop runs end-to-end: it builds the engine and drives collect local ->
// hotspots -> hypothesis -> baseline -> verdict -> report against a benchmark fixture, asserting
// report.md is produced and that `status` fails while the loop is incomplete. This guards the
// short-circuit failure mode (stopping before report.md) that the golden-scenario eval never covers.
type smokeCmd struct {
	Fixture string `default:"e2e/scenarios/string-builder-win/base" help:"fixture module dir with a benchmark"`
	Bench   string `default:"BenchmarkBuild" help:"benchmark to profile in the fixture"`
}

func (c *smokeCmd) Run() error {
	bin, tmp, err := buildEngine()
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	// copy the fixture so collect local's .go-perf-agent output does not pollute the repo
	work, err := os.MkdirTemp("", "gpa-smoke-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if err := copyDir(c.Fixture, work); err != nil {
		return fmt.Errorf("copy fixture %s: %w", c.Fixture, err)
	}

	logf("smoke: collect local (bench %s) + hotspots in a copy of %s", c.Bench, c.Fixture)
	if out, err := runSelf(bin, work, nil, "collect", "local", "--bench", c.Bench); err != nil {
		return fmt.Errorf("collect local failed: %v\n%s", err, out)
	}
	if out, err := runSelf(bin, work, nil, "hotspots"); err != nil {
		return fmt.Errorf("hotspots failed: %v\n%s", err, out)
	}

	hs := filepath.Join(work, ".go-perf-agent", "hotspots.json")
	b, err := os.ReadFile(hs)
	if err != nil {
		return fmt.Errorf("smoke: no hotspots.json produced (collect->rank front-end broken): %w", err)
	}
	if len(strings.TrimSpace(string(b))) < 3 { // "[]" or empty: nothing ranked
		return fmt.Errorf("smoke: hotspots.json is empty - front-end produced no ranked symbols")
	}
	logf("smoke: front-end OK (collect local -> hotspots, %d bytes)", len(b))

	// full loop: hypothesis -> baseline -> verdict -> report, asserting report.md is produced and
	// that `status` fails while the loop is incomplete. This is the whole-loop end-to-end gate, so a
	// short-circuit (stopping before report.md) is caught, which is the failure mode this guards.
	gdir := filepath.Dir(hs)
	if err := gitInitCommit(work, "smoke"); err != nil {
		return err
	}
	hyp := `[{"id":"smoke","pattern":"string-builder","symbol":"evalmod.Build","file":"b.go","rationale":"smoke full-loop check","metric":"B_op","benchmark":{"pkg":".","name":"BenchmarkBuild"}}]`
	if err := os.WriteFile(filepath.Join(gdir, "hypotheses.json"), []byte(hyp), 0o644); err != nil {
		return err
	}
	if out, err := runSelf(bin, work, nil, "bench", "baseline", "smoke"); err != nil {
		return fmt.Errorf("bench baseline failed: %v\n%s", err, out)
	}
	if _, err := runSelf(bin, work, nil, "status"); err == nil {
		return fmt.Errorf("smoke: `status` should fail before VALIDATE completes - the loop-completeness gate is not catching a short-circuit")
	}
	if out, err := runSelf(bin, work, []string{"GPA_BENCH_COUNT=2"}, "bench", "verdict", "smoke"); err != nil {
		return fmt.Errorf("bench verdict failed: %v\n%s", err, out)
	}
	if _, err := runSelf(bin, work, nil, "status"); err == nil {
		return fmt.Errorf("smoke: `status` should fail before report.md exists - the REPORT stage is not gated")
	}
	if out, err := runSelf(bin, work, nil, "report"); err != nil {
		return fmt.Errorf("report failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(gdir, "report.md")); err != nil {
		return fmt.Errorf("smoke: no report.md after the full loop: %w", err)
	}
	if out, err := runSelf(bin, work, nil, "status"); err != nil {
		return fmt.Errorf("smoke: `status` reports incomplete after a full loop to report.md: %v\n%s", err, out)
	}
	logf("smoke OK: full loop collect -> hotspots -> baseline -> verdict -> report.md, status complete")
	return nil
}

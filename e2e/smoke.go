package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// smokeCmd proves the collect->rank front-end runs end-to-end: it builds the engine and runs
// collect local + hotspots against a known benchmark fixture, then asserts hotspots.json is
// produced. This is the half the golden-scenario eval never exercises.
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
	logf("smoke OK: collect local -> hotspots produced %d bytes of hotspots.json", len(b))
	return nil
}

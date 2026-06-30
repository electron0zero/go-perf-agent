package main

import (
	"fmt"
	"path/filepath"

	"go-perf-agent/internal/probe"
)

type selftestCmd struct{}

// selftest: offline proof the pipeline runs without Grafana. Profiles a real benchmark in this
// repo, then runs hotspots on it. Prefers a CPU-bound micro-bench so editable hotspots surface.
func (c *selftestCmd) Run() error {
	ensureDirs()
	info("offline selftest: profiling a real benchmark in this repo")

	pkg, bench := probe.PickBenchmark(modulePath)
	if bench == "" {
		return fmt.Errorf("no benchmarks found in repo to selftest with")
	}
	info("using %s in %s", bench, pkg)
	prof := filepath.Join(gpaDir, "profiles", "selftest.prof")
	if _, _, err := run("", "go", "test", "-run=^$", "-bench=^"+bench+"$", "-benchmem",
		"-count=1", "-benchtime=1s", "-cpuprofile="+prof, pkg); err != nil {
		info("  (profile may be sparse for a fast bench; continuing)")
	}
	if err := runHotspots(prof, 10); err != nil {
		return err
	}
	info("selftest OK: pipeline collect->hotspots works offline")
	return nil
}

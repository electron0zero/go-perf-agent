package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type selftestCmd struct{}

// selftest: offline proof the pipeline runs without Grafana. Profiles a real benchmark in this
// repo, then runs hotspots on it. Prefers a CPU-bound micro-bench so editable hotspots surface.
func (c *selftestCmd) Run() error {
	ensureDirs()
	info("offline selftest: profiling a real benchmark in this repo")

	pkg, bench := pickBenchmark()
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

func pickBenchmark() (pkg, bench string) {
	// prefer a known CPU-bound micro-bench when present
	if modulePath != "" && fileExists("pkg/traceql") {
		if out, _, _ := run("", "grep", "-rl", "func BenchmarkIsAttributeRune", "pkg/traceql"); strings.TrimSpace(out) != "" {
			return "./pkg/traceql", "BenchmarkIsAttributeRune"
		}
	}
	// else: first *_test.go with a Benchmark
	out, _, _ := run("", "grep", "-rl", "--include=*_test.go", "func Benchmark", ".")
	for _, f := range strings.Split(strings.TrimSpace(out), "\n") {
		if f == "" || strings.Contains(f, "/vendor/") {
			continue
		}
		dir := "./" + filepath.Dir(f)
		if b := firstBenchmarkName(f); b != "" {
			return dir, b
		}
	}
	return "", ""
}

var benchRe = regexp.MustCompile(`func (Benchmark[A-Za-z0-9_]+)`)

func firstBenchmarkName(file string) string {
	b, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	if m := benchRe.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	return ""
}

// Package probe inspects the local module: parsing the go toolchain version and finding a
// benchmark to profile for the offline selftest.
package probe

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"go-perf-agent/internal/sh"
)

// GoMinor parses the minor version out of `go version go1.26.4 ...`.
func GoMinor(goVersion string) int {
	i := strings.Index(goVersion, "go1.")
	if i < 0 {
		return 0
	}
	rest := goVersion[i+len("go1."):]
	end := strings.IndexAny(rest, ". ")
	if end < 0 {
		return 0
	}
	n, _ := strconv.Atoi(rest[:end])
	return n
}

// PickBenchmark finds a benchmark to profile in the current module: prefers a known CPU-bound
// micro-bench when present, else the first *_test.go with a Benchmark. Returns "", "" if none.
func PickBenchmark(modulePath string) (pkg, bench string) {
	// prefer a known CPU-bound micro-bench when present
	if modulePath != "" {
		if _, err := os.Stat("pkg/traceql"); err == nil {
			if out, _, _ := sh.Run("", "grep", "-rl", "func BenchmarkIsAttributeRune", "pkg/traceql"); strings.TrimSpace(out) != "" {
				return "./pkg/traceql", "BenchmarkIsAttributeRune"
			}
		}
	}
	// else: first *_test.go with a Benchmark
	out, _, _ := sh.Run("", "grep", "-rl", "--include=*_test.go", "func Benchmark", ".")
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

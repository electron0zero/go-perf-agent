package main

import (
	"fmt"
	"go-perf-agent/internal/helper"
	"os/exec"
	"strconv"
	"strings"
)

// check verifies the external tools go-perf-agent shells out to, and feature-detects the gcx
// subcommands the production-telemetry path needs, so a run fails fast with a clear message instead
// of an opaque "exit status 1" mid-collection.
type checkCmd struct{}

func (c *checkCmd) Run() error {
	info("checking tools go-perf-agent needs...")
	missing := false

	for _, t := range []struct{ name, why string }{
		{"go", "build and run benchmarks (1.23+)"},
		{"git", "per-hypothesis worktrees (2.5+)"},
		{"benchstat", "the numeric gate (go install golang.org/x/perf/cmd/benchstat@latest)"},
	} {
		if _, err := exec.LookPath(t.name); err != nil {
			info("  MISSING  %-9s required: %s", t.name, t.why)
			missing = true
		} else {
			info("  ok       %-9s %s", t.name, t.why)
		}
	}
	if out, _, err := helper.Run("", "go", "version"); err == nil {
		if v := goMinor(out); v > 0 && v < 23 {
			info("  WARN     go is 1.%d; go-perf-agent needs Go 1.23+", v)
			missing = true
		}
	}

	if _, err := exec.LookPath("gh"); err != nil {
		info("  note     gh        not found - only needed for `target-diff --pr`")
	} else {
		info("  ok       gh        PR diff mode")
	}

	if _, err := exec.LookPath("gcx"); err != nil {
		info("  note     gcx       not found - production telemetry unavailable; `collect local` (go pprof) still works")
	} else {
		checkGcx()
	}

	if missing {
		return fmt.Errorf("missing required tools (see above)")
	}
	info("required tools present")
	info("  tip: benchmark on an idle machine, and laptops connected to AC power, with Browser/IDE closed")
	return nil
}

// checkGcx feature-detects the subcommands the production path needs. Feature detection is more
// robust than parsing a version string; it tells the user exactly what to upgrade.
func checkGcx() {
	const upgrade = "upgrade gcx to v0.4.2+ (go install github.com/grafana/gcx/cmd/gcx@latest)"
	for _, ck := range []struct {
		args   []string
		want   string
		absent string // substring that means the feature is ABSENT (else require want present)
		label  string
	}{
		{[]string{"datasources", "tempo", "query", "--help"}, "", "not yet implemented", "tempo query (traces)"},
		{[]string{"datasources", "pyroscope", "exemplars", "--help"}, "exemplars", "", "pyroscope exemplars (span/profile pivot)"},
		{[]string{"datasources", "pyroscope", "query", "--help"}, "pprof", "", "pyroscope query -o pprof (profiles)"},
	} {
		out, errOut, _ := helper.Run("", "gcx", ck.args...)
		s := out + errOut
		bad := (ck.absent != "" && strings.Contains(s, ck.absent)) || (ck.want != "" && !strings.Contains(s, ck.want))
		if bad {
			info("  WARN     gcx: %s unavailable - %s", ck.label, upgrade)
		} else {
			info("  ok       gcx       %s", ck.label)
		}
	}
}

// goMinor parses the minor version out of `go version go1.26.4 ...`.
func goMinor(goVersion string) int {
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

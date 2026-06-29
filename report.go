package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type reportCmd struct{}

func (c *reportCmd) Run() error {
	runsGlob := filepath.Join(gpaDir, "runs", "*", "verdict.json")
	files, _ := filepath.Glob(runsGlob)
	sort.Strings(files)

	var b strings.Builder
	b.WriteString("# go-perf-agent report\n\n")
	b.WriteString(fmt.Sprintf("Generated from `%s/runs/*/verdict.json`. Findings are LLM-assisted hypotheses - validate each proved change in production before trusting it.\n\n", gpaDir))
	b.WriteString("| status | id | metric Δ | p | reason |\n|---|---|---|---|---|\n")

	var verdicts []Verdict
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var v Verdict
		if json.Unmarshal(raw, &v) != nil {
			continue
		}
		verdicts = append(verdicts, v)
		delta, p, reason := "-", "-", "-"
		if v.Verdict != nil {
			if v.Verdict.Delta != "" {
				delta = v.Verdict.Delta
			}
			if v.Verdict.PValue != "" {
				p = v.Verdict.PValue
			}
			if v.Verdict.Reason != "" {
				reason = v.Verdict.Reason
			}
		}
		if reason == "-" && v.Reason != "" {
			reason = v.Reason
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s |\n", v.Status, v.ID, delta, p, reason))
	}

	b.WriteString(telemetryCoverage())

	b.WriteString("\n## Proved hypotheses (ship behind a flag, then verify in production)\n\n")
	for _, v := range verdicts {
		if v.Status != "proved" || v.Verdict == nil {
			continue
		}
		b.WriteString(fmt.Sprintf("### %s\n\n```\n%s\n```\n", v.ID, strings.TrimRight(v.Verdict.Benchstat, "\n")))
		// diff HEAD (not plain diff) so the staged, newly-authored benchmark is included in the patch
		b.WriteString(fmt.Sprintf("Worktree: `%s` (review the full patch, incl. the authored benchmark, with `git -C %s diff HEAD`)\n\n", v.Verdict.Worktree, v.Verdict.Worktree))
	}

	out := filepath.Join(gpaDir, "report.md")
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		return err
	}
	info("wrote %s", out)
	fmt.Fprint(os.Stderr, b.String())
	return nil
}

// telemetryCoverage inspects what was actually collected and reports infra/telemetry gaps, so the
// user knows the tool ran on partial data and what would make it more precise. Grounded in the
// Tempo run, where the heavy service lacked span profiles and the precise pivot fell back.
func telemetryCoverage() string {
	prof, _ := filepath.Glob(filepath.Join(gpaDir, "profiles", "*.pb.gz"))
	traces, _ := filepath.Glob(filepath.Join(gpaDir, "traces", "*.json"))
	exFiles, _ := filepath.Glob(filepath.Join(gpaDir, "profiles", "*.exemplars.*.json"))

	prodProfiles := false
	for _, p := range prof {
		if !strings.HasPrefix(filepath.Base(p), "local.") {
			prodProfiles = true
		}
	}
	spanLinked := false
	for _, f := range exFiles {
		var r exemplarsResult
		if raw, err := os.ReadFile(f); err == nil && json.Unmarshal(raw, &r) == nil && len(r.Exemplars) > 0 {
			spanLinked = true
		}
	}

	var missing []string
	if len(traces) == 0 {
		missing = append(missing, "production traces (Tempo) - without them the traces-first step can't localize the slow operation")
	}
	if !prodProfiles {
		missing = append(missing, "production profiles (Pyroscope) - only local profiles were used, which miss real input distributions and load")
	}
	if len(exFiles) > 0 && !spanLinked {
		missing = append(missing, "span profiles (otel-profiling-go) on the hot service - exemplars came back empty, so the precise trace->profile pivot fell back to the service-wide profile")
	}

	var b strings.Builder
	b.WriteString("\n## Telemetry coverage\n\n")
	if len(missing) == 0 {
		b.WriteString("Full coverage: production traces, profiles, and span-linked profiles were available.\n")
		return b.String()
	}
	b.WriteString("go-perf-agent works best when production telemetry includes Tempo traces, Pyroscope profiles, and span profiles (otel-profiling-go) on the hot services. This run was missing:\n\n")
	for _, m := range missing {
		b.WriteString("- " + m + "\n")
	}
	b.WriteString("\nResults are still valid but less precise; closing these gaps improves localization.\n")
	return b.String()
}

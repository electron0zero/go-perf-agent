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

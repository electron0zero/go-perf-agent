// Package report renders the per-hypothesis verdicts under .go-perf-agent/runs into the human
// report.md, including a telemetry-coverage section that flags what data was missing.
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go-perf-agent/internal/model"
)

// Render builds report.md from dir/runs/*/verdict.json and returns the markdown (no file writes).
func Render(dir string) (string, error) {
	files, _ := filepath.Glob(filepath.Join(dir, "runs", "*", "verdict.json"))
	// loop-completeness gate: no verdicts means VALIDATE never ran. Fail loud instead of emitting an
	// empty report, so the loop is not silently abandoned after collect/hotspots.
	if len(files) == 0 {
		return "", fmt.Errorf("no verdicts under %s/runs/ - the VALIDATE stage has not run; form %s/hypotheses.json and run `validate`/`bench` for each candidate before `report` (do not hand-write the analysis in place of the gate)", dir, dir)
	}
	sort.Strings(files)

	var b strings.Builder
	b.WriteString("# go-perf-agent report\n\n")
	b.WriteString(fmt.Sprintf("Generated from `%s/runs/*/verdict.json`. Findings are LLM-assisted hypotheses - validate each proved change in production before trusting it.\n\n", dir))
	var verdicts []model.Verdict
	var rows [][5]string // status, id, delta, p, reason - reason last so it can flow unpadded
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var v model.Verdict
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
		rows = append(rows, [5]string{v.Status, v.ID, delta, p, reason})
	}
	b.WriteString(verdictTable(rows))

	b.WriteString(telemetryCoverage(dir))

	b.WriteString("\n## Proved hypotheses (ship behind a flag, then verify in production)\n\n")
	for _, v := range verdicts {
		if v.Status != "proved" || v.Verdict == nil {
			continue
		}
		b.WriteString(fmt.Sprintf("### %s\n\n```\n%s\n```\n", v.ID, strings.TrimRight(v.Verdict.Benchstat, "\n")))
		// diff HEAD (not plain diff) so the staged, newly-authored benchmark is included in the patch
		b.WriteString(fmt.Sprintf("Worktree: `%s` (review the full patch, incl. the authored benchmark, with `git -C %s diff HEAD`)\n\n", v.Verdict.Worktree, v.Verdict.Worktree))
	}
	return b.String(), nil
}

// verdictTable renders the verdict summary as a markdown table with the fixed columns padded to their
// widest cell, so it lines up as plain text and not only when rendered. reason is last and left free.
func verdictTable(rows [][5]string) string {
	head := [5]string{"status", "id", "delta", "p", "reason"}
	w := [5]int{}
	for i, h := range head {
		w[i] = len(h)
	}
	for _, r := range rows {
		for i := 0; i < 4; i++ { // reason (last) is not measured, it flows
			if len(r[i]) > w[i] {
				w[i] = len(r[i])
			}
		}
	}
	line := func(cells [5]string) string {
		var sb strings.Builder
		sb.WriteByte('|')
		for i, c := range cells {
			if i < 4 {
				c += strings.Repeat(" ", w[i]-len(c))
			}
			sb.WriteString(" " + c + " |")
		}
		sb.WriteByte('\n')
		return sb.String()
	}
	var sep [5]string
	for i := range sep {
		n := w[i]
		if i == 4 {
			n = len(head[i])
		}
		sep[i] = strings.Repeat("-", n)
	}
	var b strings.Builder
	b.WriteString(line(head))
	b.WriteString(line(sep))
	for _, r := range rows {
		b.WriteString(line(r))
	}
	return b.String()
}

// exemplars is the slice of an exemplars file we need: just how many came back.
type exemplars struct {
	Exemplars []struct {
		ProfileID string `json:"profileId"`
		SpanID    string `json:"spanId"`
		Value     int64  `json:"value"`
	} `json:"exemplars"`
}

// telemetryCoverage inspects what was actually collected and reports infra/telemetry gaps, so the
// user knows the tool ran on partial data and what would make it more precise. Grounded in the
// Tempo run, where the heavy service lacked span profiles and the precise pivot fell back.
func telemetryCoverage(dir string) string {
	prof, _ := filepath.Glob(filepath.Join(dir, "profiles", "*.pb.gz"))
	traces, _ := filepath.Glob(filepath.Join(dir, "traces", "*.json"))
	exFiles, _ := filepath.Glob(filepath.Join(dir, "profiles", "*.exemplars.*.json"))

	prodProfiles := false
	for _, p := range prof {
		if !strings.HasPrefix(filepath.Base(p), "local.") {
			prodProfiles = true
		}
	}
	spanLinked := false
	for _, f := range exFiles {
		var r exemplars
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

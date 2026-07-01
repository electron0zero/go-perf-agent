// Package report renders the per-hypothesis verdicts under .go-perf-agent/runs into the human
// report.md, including a telemetry-coverage section that flags what data was missing.
package report

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"go-perf-agent/internal/helper"
	"go-perf-agent/internal/model"
)

// report.tmpl is the markdown skeleton. Editing the layout is a template change, not a code change.
// The computed blocks (the padded verdict table, the telemetry-coverage section) are rendered by the
// helpers below and placed into the template as-is.
//
//go:embed report.tmpl
var reportTmplText string

var reportTmpl = template.Must(template.New("report").Parse(reportTmplText))

// Meta is the frontmatter the caller supplies (it knows the clock, cwd, and git HEAD, which the pure
// renderer should not reach for). ClosingNote is optional - empty leaves a placeholder to fill.
type Meta struct {
	Date        string
	Repo        string
	Commit      string
	ClosingNote string
}

// reportView is the data report.tmpl renders.
type reportView struct {
	Date          string
	Repo          string
	Commit        string
	VerdictTable  string
	TelemetryData string
	Coverage      string
	Proved        []provedView
	ClosingNotes  string
}

type provedView struct{ ID, Benchstat, Worktree, PatchFile string }

// Render builds report.md from dir/runs/*/verdict.json and returns the markdown (no file writes -
// call WritePatches first to produce the per-proved patch files it references).
func Render(dir string, meta Meta) (string, error) {
	files, _ := filepath.Glob(filepath.Join(dir, "runs", "*", "verdict.json"))
	// loop-completeness gate: no verdicts means VALIDATE never ran. Fail loud instead of emitting an
	// empty report, so the loop is not silently abandoned after collect/hotspots.
	if len(files) == 0 {
		return "", fmt.Errorf("no verdicts under %s/runs/ - the VALIDATE stage has not run; form %s/hypotheses.json and run `validate`/`bench` for each candidate before `report` (do not hand-write the analysis in place of the gate)", dir, dir)
	}
	sort.Strings(files)

	var rows [][5]string // status, id, delta, p, reason - reason last so it can flow unpadded
	var proved []provedView
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var v model.Verdict
		if json.Unmarshal(raw, &v) != nil {
			continue
		}
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
		if v.Status == "proved" && v.Verdict != nil {
			proved = append(proved, provedView{v.ID, strings.TrimRight(v.Verdict.Benchstat, "\n"), v.Verdict.Worktree, patchPath(dir, v.ID)})
		}
	}

	closing := meta.ClosingNote
	if closing == "" {
		closing = "_(fill in: the overall takeaway - what to ship first, and what to re-check in production.)_"
	}
	view := reportView{
		Date:          meta.Date,
		Repo:          meta.Repo,
		Commit:        meta.Commit,
		VerdictTable:  strings.TrimRight(verdictTable(rows), "\n"),
		TelemetryData: strings.TrimSpace(telemetryData(dir)),
		Coverage:      strings.TrimSpace(telemetryCoverage(dir)),
		Proved:        proved,
		ClosingNotes:  closing,
	}
	var b strings.Builder
	if err := reportTmpl.Execute(&b, view); err != nil {
		return "", err
	}
	return b.String(), nil
}

// patchPath is the report-relative location of a proved hypothesis's patch file.
func patchPath(dir, id string) string { return filepath.Join(dir, "patches", id+".patch") }

// WritePatches writes a git patch (diff HEAD, so the staged authored benchmark is included) for each
// proved hypothesis's worktree to dir/patches/<id>.patch, so an engineer can inspect the change and
// `git apply` it on the target commit without the worktree.
func WritePatches(dir string) error {
	files, _ := filepath.Glob(filepath.Join(dir, "runs", "*", "verdict.json"))
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var v model.Verdict
		if json.Unmarshal(raw, &v) != nil || v.Status != "proved" || v.Verdict == nil || v.Verdict.Worktree == "" {
			continue
		}
		diff, _, err := helper.Run(v.Verdict.Worktree, "git", "diff", "HEAD")
		if err != nil {
			continue // a stale/removed worktree just has no patch file
		}
		if err := os.MkdirAll(filepath.Join(dir, "patches"), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(patchPath(dir, v.ID), []byte(diff), 0o644); err != nil {
			return err
		}
	}
	return nil
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

// telemetrySummary is the slice of gpa-query-telemetry's summary.json we render: where the signals
// came from, the window, and the queries that produced them.
type telemetrySummary struct {
	Source          string `json:"source"`
	Service         string `json:"service"`
	Window          string `json:"window"`
	DeployedVersion string `json:"deployed_version"`
	Signals         []struct {
		Kind   string `json:"kind"`
		Metric string `json:"metric"`
		Query  string `json:"query"`
	} `json:"signals"`
}

// telemetryData reports which source, time range, and queries produced the traces/profiles. It reads
// gpa-query-telemetry's summary.json when present, else derives what it can from the collected files
// (so a direct collect run, without the agent, still records what was queried).
func telemetryData(dir string) string {
	var b strings.Builder
	b.WriteString("## Telemetry Data\n\n")
	if raw, err := os.ReadFile(filepath.Join(dir, "telemetry", "summary.json")); err == nil {
		var s telemetrySummary
		if json.Unmarshal(raw, &s) == nil {
			src := s.Source
			if src == "" {
				src = "unknown"
			}
			b.WriteString("Source: " + src + "\n")
			for _, kv := range [][2]string{{"Service", s.Service}, {"Window", s.Window}, {"Deployed version", s.DeployedVersion}} {
				if kv[1] != "" {
					b.WriteString(kv[0] + ": " + kv[1] + "\n")
				}
			}
			if len(s.Signals) > 0 {
				b.WriteString("\nQueries:\n")
				for _, sig := range s.Signals {
					if sig.Query == "" {
						continue
					}
					label := strings.TrimSpace(sig.Kind + " " + sig.Metric)
					b.WriteString(fmt.Sprintf("- %s: `%s`\n", label, sig.Query))
				}
			}
			return b.String()
		}
	}
	// no summary.json (collection run directly, not via gpa-query-telemetry) - derive from disk.
	b.WriteString("No telemetry summary recorded (collection run directly, not via gpa-query-telemetry). From disk:\n\n")
	profs, _ := filepath.Glob(filepath.Join(dir, "profiles", "*.pb.gz"))
	locals, _ := filepath.Glob(filepath.Join(dir, "profiles", "*.prof"))
	for _, p := range append(profs, locals...) {
		b.WriteString("- profile: `" + filepath.Base(p) + "`\n")
	}
	for _, t := range mustGlob(filepath.Join(dir, "traces", "*.json")) {
		b.WriteString("- trace dump: `" + filepath.Base(t) + "`\n")
	}
	if dv, err := os.ReadFile(filepath.Join(dir, "deployed_version")); err == nil {
		b.WriteString("- deployed version: " + strings.TrimSpace(string(dv)) + "\n")
	}
	return b.String()
}

func mustGlob(pat string) []string { m, _ := filepath.Glob(pat); return m }

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

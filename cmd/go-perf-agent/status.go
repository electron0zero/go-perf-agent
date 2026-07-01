package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"go-perf-agent/internal/model"
	"go-perf-agent/internal/report"
)

// status reports how far the audit loop has run in the working dir and exits non-zero when it is
// incomplete, so a short-circuit (stopping before report.md) is caught deterministically. With an
// id it prints that hypothesis's recorded verdict instead - the authoritative status reader.
type statusCmd struct {
	ID string `arg:"" optional:"" help:"a hypothesis id to print its recorded verdict, or omit for the whole-loop overview"`
}

func (c *statusCmd) Run() error {
	if c.ID != "" {
		return showVerdict(c.ID)
	}
	s := report.LoopStatus(gpaDir)
	info("profiles=%d candidates=%d hypotheses=%d verdicts=%d report=%v deployed_version=%v",
		s.Profiles, s.Candidates, s.Hypotheses, s.VerdictTotal(), s.Report, s.Deployed)
	for _, st := range []string{"proved", "rejected", "need_more_data"} {
		if n := s.Verdicts[st]; n > 0 {
			info("  %-14s %d", st, n)
		}
	}
	if ok, reason := s.Complete(); !ok {
		return fmt.Errorf("loop incomplete: %s", reason)
	}
	info("loop complete: report.md written with a verdict per hypothesis")
	return nil
}

// showVerdict prints one hypothesis's recorded verdict from runs/<id>/verdict.json.
func showVerdict(id string) error {
	b, err := os.ReadFile(filepath.Join(gpaDir, "runs", id, "verdict.json"))
	if err != nil {
		return fmt.Errorf("no verdict for %s - run bench verdict first", id)
	}
	var v model.Verdict
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	delta, p, reason := "-", "-", v.Reason
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
	info("%s: %s (delta=%s p=%s) %s", id, v.Status, delta, p, reason)
	return nil
}

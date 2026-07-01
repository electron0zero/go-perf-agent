package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"go-perf-agent/internal/model"
)

// LoopState is a deterministic snapshot of how far the audit loop has run in a .go-perf-agent dir,
// so a short-circuit (stopping before report.md) is detectable without re-running anything.
type LoopState struct {
	Profiles   int
	Candidates int
	Hypotheses int
	Verdicts   map[string]int // status -> count
	Report     bool
	Deployed   bool
}

// LoopStatus inspects dir and reports which stages have produced their artifacts.
func LoopStatus(dir string) LoopState {
	s := LoopState{Verdicts: map[string]int{}}
	for _, g := range []string{"*.pb.gz", "*.prof"} {
		m, _ := filepath.Glob(filepath.Join(dir, "profiles", g))
		s.Profiles += len(m)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "hotspots.json")); err == nil {
		var hs []struct {
			Candidate bool `json:"candidate"`
		}
		if json.Unmarshal(b, &hs) == nil {
			for _, h := range hs {
				if h.Candidate {
					s.Candidates++
				}
			}
		}
	}
	if hs, err := model.LoadHypotheses(dir); err == nil {
		s.Hypotheses = len(hs)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "runs", "*", "verdict.json"))
	for _, f := range files {
		var v model.Verdict
		if b, err := os.ReadFile(f); err == nil && json.Unmarshal(b, &v) == nil {
			s.Verdicts[v.Status]++
		}
	}
	s.Report = fileExists(filepath.Join(dir, "report.md"))
	s.Deployed = fileExists(filepath.Join(dir, "deployed_version"))
	return s
}

// VerdictTotal is the number of verdicts written across all statuses.
func (s LoopState) VerdictTotal() int {
	n := 0
	for _, c := range s.Verdicts {
		n += c
	}
	return n
}

// Complete reports whether the loop reached report.md with a verdict for every hypothesis. The
// reason names the first unfinished stage otherwise, so a stalled loop points at what to run next.
func (s LoopState) Complete() (bool, string) {
	switch {
	case s.Candidates == 0 && s.Hypotheses == 0 && s.VerdictTotal() == 0:
		return false, "not started - run collect then hotspots"
	case s.Candidates > 0 && s.Hypotheses == 0:
		return false, "hotspots ranked candidates but hypotheses.json is empty - HYPOTHESIZE not run"
	case s.Hypotheses > 0 && s.VerdictTotal() < s.Hypotheses:
		return false, fmt.Sprintf("%d hypotheses but %d verdicts - VALIDATE incomplete", s.Hypotheses, s.VerdictTotal())
	case s.VerdictTotal() > 0 && !s.Report:
		return false, "verdicts exist but no report.md - REPORT not run"
	case !s.Report:
		return false, "no report.md - REPORT not run"
	}
	return true, ""
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

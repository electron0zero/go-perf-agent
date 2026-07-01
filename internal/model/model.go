// Package model holds the JSON contract between the LLM (which authors hypotheses) and the
// deterministic harness (which validates them), plus the small file store that persists it.
package model

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Benchmark struct {
	Pkg            string `json:"pkg"`
	Name           string `json:"name,omitempty"`
	NeedsAuthoring bool   `json:"needs_authoring,omitempty"`
}

// Dependency marks a hypothesis whose fix lives in a dependency we don't own but can change: a
// vendored OSS module (in-tree, so benchmarkable) or generated code. It is a normal hypothesis -
// the harness just requires the user to opt in (put Path in scope) before validating it, and the
// change must be upstreamed or carried as a vendor patch to actually ship.
type Dependency struct {
	Path     string `json:"path"`               // in-tree dir to edit, e.g. vendor/github.com/parquet-go/parquet-go
	Kind     string `json:"kind"`               // vendored-oss | generated
	Upstream string `json:"upstream,omitempty"` // where to upstream it, e.g. github.com/parquet-go/parquet-go
}

// Hypothesis matches schema/hypothesis.schema.json. Authored by the LLM, consumed by the harness.
// Status is owned by the per-run verdict.json, not tracked here.
type Hypothesis struct {
	ID         string          `json:"id"`
	Pattern    string          `json:"pattern"`
	Symbol     string          `json:"symbol"`
	File       string          `json:"file,omitempty"`
	Line       int             `json:"line,omitempty"`
	Evidence   json.RawMessage `json:"evidence,omitempty"`
	Rationale  string          `json:"rationale"`
	Metric     string          `json:"metric"` // ns_op | B_op | allocs_op
	Benchmark  Benchmark       `json:"benchmark"`
	Dependency *Dependency     `json:"dependency,omitempty"` // set when the fix is in a dependency
	Risk       string          `json:"risk,omitempty"`
}

type VerdictDetail struct {
	Kept        bool   `json:"kept"`
	TestsPassed bool   `json:"tests_passed"`
	Delta       string `json:"delta"`
	PValue      string `json:"p_value"`
	Reason      string `json:"reason"`
	Worktree    string `json:"worktree"`
	Benchstat   string `json:"benchstat"`
}

// Verdict is written to .go-perf-agent/runs/<id>/verdict.json. status is the final stage:
// proved | rejected | need_more_data.
type Verdict struct {
	ID      string         `json:"id"`
	Status  string         `json:"status"`
	Reason  string         `json:"reason,omitempty"`  // top-level reason for need_more_data placeholders
	Verdict *VerdictDetail `json:"verdict,omitempty"` // filled by the gate
	Critic  *CriticReview  `json:"critic,omitempty"`  // filled by the reflexion critic pass
}

// CriticReview records a structurally-distinct critique. The critic may only downgrade a PROVED
// (semantic/behavior concerns the numeric gate cannot see) - it can never promote a rejection.
type CriticReview struct {
	Passed bool   `json:"passed"`
	Reason string `json:"reason"`
}

// ApplyCritic records the critic's review on v and downgrades a PROVED verdict to need_more_data
// when the critic rejects it (a veto on wins the numeric gate cannot see - never promotes a
// rejection). Returns true iff it downgraded.
func (v *Verdict) ApplyCritic(reject bool, reason string) (downgraded bool) {
	v.Critic = &CriticReview{Passed: !reject, Reason: reason}
	if reject && v.Status == "proved" {
		if v.Verdict != nil {
			v.Verdict.Reason = "downgraded by critic: " + reason + " (gate said: " + v.Verdict.Reason + ")"
		}
		v.Status = "need_more_data"
		return true
	}
	return false
}

// ---- store helpers ----------------------------------------------------------------------

func LoadHypotheses(dir string) ([]Hypothesis, error) {
	b, err := os.ReadFile(filepath.Join(dir, "hypotheses.json"))
	if err != nil {
		return nil, fmt.Errorf("no %s/hypotheses.json", dir)
	}
	var hs []Hypothesis
	if err := json.Unmarshal(b, &hs); err != nil {
		return nil, fmt.Errorf("parse hypotheses.json: %w", err)
	}
	return hs, nil
}

func GetHypothesis(dir, id string) (*Hypothesis, error) {
	hs, err := LoadHypotheses(dir)
	if err != nil {
		return nil, err
	}
	for i := range hs {
		if hs[i].ID == id {
			return &hs[i], nil
		}
	}
	return nil, fmt.Errorf("no hypothesis %s in %s/hypotheses.json", id, dir)
}

// SetBenchmarkName records an authored benchmark on a hypothesis (name + clears needs_authoring), so
// a bench baseline re-run after authoring proceeds without the agent hand-editing hypotheses.json.
func SetBenchmarkName(dir, id, name string) error {
	hs, err := LoadHypotheses(dir)
	if err != nil {
		return err
	}
	for i := range hs {
		if hs[i].ID == id {
			hs[i].Benchmark.Name = name
			hs[i].Benchmark.NeedsAuthoring = false
			return WriteJSON(filepath.Join(dir, "hypotheses.json"), hs)
		}
	}
	return fmt.Errorf("no hypothesis %s in %s/hypotheses.json", id, dir)
}

func WriteJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

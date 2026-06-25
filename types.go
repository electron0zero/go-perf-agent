package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Scope: which parts of the codebase are in/out of bounds. Entries are path prefixes relative
// to the module root (e.g. "pkg/parquet", "tempodb"); a trailing "/..." is ignored.
type Scope struct {
	Root    string   `json:"root"`
	Include []string `json:"include"`
	Exclude []string `json:"exclude"`
}

// Hotspot: one ranked hot symbol. candidate = editable (ours, not stdlib/vendor) AND in_scope.
type Hotspot struct {
	Rank      int     `json:"rank"`
	Symbol    string  `json:"symbol"`
	Package   string  `json:"package"`
	WeightPct float64 `json:"weight_pct"`
	Metric    string  `json:"metric"`
	Source    string  `json:"source"`
	Editable  bool    `json:"editable"`
	InScope   bool    `json:"in_scope"`
	Candidate bool    `json:"candidate"`
}

type Benchmark struct {
	Pkg            string `json:"pkg"`
	Name           string `json:"name,omitempty"`
	NeedsAuthoring bool   `json:"needs_authoring,omitempty"`
}

// Dependency marks a hypothesis whose fix lives in a dependency we don't own but can change: a
// vendored OSS module (in-tree, so benchmarkable) or generated code. It is a normal hypothesis;
// the harness just requires the user to opt in (put Path in scope) before validating it, and the
// change must be upstreamed or carried as a vendor patch to actually ship.
type Dependency struct {
	Path     string `json:"path"`               // in-tree dir to edit, e.g. vendor/github.com/parquet-go/parquet-go
	Kind     string `json:"kind"`               // vendored-oss | generated
	Upstream string `json:"upstream,omitempty"` // where to upstream it, e.g. github.com/parquet-go/parquet-go
}

// Hypothesis matches schema/hypothesis.schema.json. Authored by the LLM; consumed by the harness.
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
	Status     string          `json:"status,omitempty"`
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
// (semantic/behavior concerns the numeric gate cannot see); it can never promote a rejection.
type CriticReview struct {
	Passed bool   `json:"passed"`
	Reason string `json:"reason"`
}

// ---- store helpers ----------------------------------------------------------------------

func loadScope() *Scope {
	b, err := os.ReadFile(filepath.Join(gpaDir, "scope.json"))
	if err != nil {
		return nil
	}
	var s Scope
	if json.Unmarshal(b, &s) != nil {
		return nil
	}
	return &s
}

func loadHypotheses() ([]Hypothesis, error) {
	b, err := os.ReadFile(filepath.Join(gpaDir, "hypotheses.json"))
	if err != nil {
		return nil, fmt.Errorf("no %s/hypotheses.json", gpaDir)
	}
	var hs []Hypothesis
	if err := json.Unmarshal(b, &hs); err != nil {
		return nil, fmt.Errorf("parse hypotheses.json: %w", err)
	}
	return hs, nil
}

func getHypothesis(id string) (*Hypothesis, error) {
	hs, err := loadHypotheses()
	if err != nil {
		return nil, err
	}
	for i := range hs {
		if hs[i].ID == id {
			return &hs[i], nil
		}
	}
	return nil, fmt.Errorf("no hypothesis %s in %s/hypotheses.json", id, gpaDir)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

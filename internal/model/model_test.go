package model

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	hs := []Hypothesis{
		{ID: "h1", Pattern: "string-builder", Symbol: "pkg.Foo", Metric: "B_op"},
		{ID: "h2", Pattern: "preallocate", Symbol: "pkg.Bar", Metric: "allocs_op"},
	}
	if err := WriteJSON(filepath.Join(dir, "hypotheses.json"), hs); err != nil {
		t.Fatal(err)
	}
	got, err := LoadHypotheses(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "h1" || got[1].Metric != "allocs_op" {
		t.Fatalf("LoadHypotheses = %+v", got)
	}

	h, err := GetHypothesis(dir, "h2")
	if err != nil || h.Symbol != "pkg.Bar" {
		t.Fatalf("GetHypothesis(h2) = %+v, %v", h, err)
	}
	if _, err := GetHypothesis(dir, "nope"); err == nil {
		t.Error("GetHypothesis(missing) should error")
	}
	if _, err := LoadHypotheses(t.TempDir()); err == nil {
		t.Error("LoadHypotheses with no file should error")
	}
}

func TestApplyCritic(t *testing.T) {
	// reject on a PROVED downgrades to need_more_data and rewrites the gate reason
	v := Verdict{ID: "h1", Status: "proved", Verdict: &VerdictDetail{Reason: "significant improvement"}}
	if !v.ApplyCritic(true, "behavior changed") {
		t.Fatal("reject on proved should downgrade")
	}
	if v.Status != "need_more_data" {
		t.Errorf("status = %q, want need_more_data", v.Status)
	}
	if v.Critic == nil || v.Critic.Passed {
		t.Errorf("critic = %+v, want recorded + not passed", v.Critic)
	}
	if !strings.Contains(v.Verdict.Reason, "downgraded by critic") || !strings.Contains(v.Verdict.Reason, "significant improvement") {
		t.Errorf("reason should fold in critic + gate reason, got %q", v.Verdict.Reason)
	}

	// reject on a non-proved verdict only notes it; never changes status
	r := Verdict{ID: "h2", Status: "rejected"}
	if r.ApplyCritic(true, "still bad") {
		t.Error("reject on rejected should not downgrade")
	}
	if r.Status != "rejected" {
		t.Errorf("status = %q, want rejected (never promoted)", r.Status)
	}

	// pass records a passing review, no status change
	p := Verdict{ID: "h3", Status: "proved", Verdict: &VerdictDetail{Reason: "win"}}
	if p.ApplyCritic(false, "") || p.Status != "proved" || p.Critic == nil || !p.Critic.Passed {
		t.Errorf("pass should record passed critic and keep proved, got %+v", p)
	}
}

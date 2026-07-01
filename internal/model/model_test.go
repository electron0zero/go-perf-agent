package model

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	hs := []Hypothesis{
		{ID: "h1", Pattern: "string-builder", Symbol: "pkg.Foo", Metric: "B_op"},
		{ID: "h2", Pattern: "preallocate", Symbol: "pkg.Bar", Metric: "allocs_op"},
	}
	require.NoError(t, WriteJSON(filepath.Join(dir, "hypotheses.json"), hs))

	got, err := LoadHypotheses(dir)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "h1", got[0].ID)
	require.Equal(t, "allocs_op", got[1].Metric)

	h, err := GetHypothesis(dir, "h2")
	require.NoError(t, err)
	require.Equal(t, "pkg.Bar", h.Symbol)

	_, err = GetHypothesis(dir, "nope")
	require.Error(t, err, "missing hypothesis should error")
	_, err = LoadHypotheses(t.TempDir())
	require.Error(t, err, "no file should error")
}

func TestSetBenchmarkName(t *testing.T) {
	dir := t.TempDir()
	hs := []Hypothesis{{ID: "h1", Benchmark: Benchmark{Pkg: "./pkg", NeedsAuthoring: true}}}
	require.NoError(t, WriteJSON(filepath.Join(dir, "hypotheses.json"), hs))

	require.NoError(t, SetBenchmarkName(dir, "h1", "BenchmarkFoo"))
	got, err := GetHypothesis(dir, "h1")
	require.NoError(t, err)
	require.Equal(t, "BenchmarkFoo", got.Benchmark.Name)
	require.False(t, got.Benchmark.NeedsAuthoring, "authoring resolved")

	require.Error(t, SetBenchmarkName(dir, "nope", "X"), "unknown id errors")
}

func TestApplyCritic(t *testing.T) {
	// reject on a PROVED downgrades to need_more_data and folds the gate reason in
	v := Verdict{ID: "h1", Status: "proved", Verdict: &VerdictDetail{Reason: "significant improvement"}}
	require.True(t, v.ApplyCritic(true, "behavior changed"), "reject on proved downgrades")
	require.Equal(t, "need_more_data", v.Status)
	require.NotNil(t, v.Critic)
	require.False(t, v.Critic.Passed)
	require.Contains(t, v.Verdict.Reason, "downgraded by critic")
	require.Contains(t, v.Verdict.Reason, "significant improvement")

	// reject on a non-proved verdict only notes it - never changes status
	r := Verdict{ID: "h2", Status: "rejected"}
	require.False(t, r.ApplyCritic(true, "still bad"), "reject on rejected does not downgrade")
	require.Equal(t, "rejected", r.Status, "critic never promotes")

	// pass records a passing review, no status change
	p := Verdict{ID: "h3", Status: "proved", Verdict: &VerdictDetail{Reason: "win"}}
	require.False(t, p.ApplyCritic(false, ""))
	require.Equal(t, "proved", p.Status)
	require.NotNil(t, p.Critic)
	require.True(t, p.Critic.Passed)
}

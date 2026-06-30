package hotspot

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const mod = "github.com/grafana/tempo"

// Rank: a symbol hot in two metrics keeps a row for EACH (regression test for the cross-metric
// blend bug that silently dropped the lower-pct metric).
func TestRankKeepsBothMetrics(t *testing.T) {
	samples := []Sample{
		{Value: 10, Symbol: mod + "/pkg/a.F", Metric: "alloc", Source: "pyroscope"},
		{Value: 5, Symbol: mod + "/pkg/a.F", Metric: "inuse", Source: "pyroscope"},
		{Value: 30, Symbol: mod + "/pkg/b.G", Metric: "alloc", Source: "pyroscope"},
	}
	hots := Rank(samples, nil, mod)

	type sm struct{ symbol, metric string }
	byKey := map[sm]Hotspot{}
	for _, h := range hots {
		byKey[sm{h.Symbol, h.Metric}] = h
	}
	require.Len(t, hots, 3, "a/alloc, a/inuse, b/alloc")
	// per-metric normalization: alloc total = 40, inuse total = 5
	require.Equal(t, 25.0, byKey[sm{mod + "/pkg/a.F", "alloc"}].WeightPct)
	require.Equal(t, 75.0, byKey[sm{mod + "/pkg/b.G", "alloc"}].WeightPct)
	require.Equal(t, 100.0, byKey[sm{mod + "/pkg/a.F", "inuse"}].WeightPct, "its own metric")
}

// Rank sums the same symbol across multiple profiles of the same metric (multi-service) by absolute
// value, instead of comparing non-comparable per-file percentages.
func TestRankAggregatesAcrossFiles(t *testing.T) {
	samples := []Sample{
		{Value: 10, Symbol: mod + "/pkg/a.F", Metric: "alloc", Source: "pyroscope"}, // service 1
		{Value: 20, Symbol: mod + "/pkg/a.F", Metric: "alloc", Source: "pyroscope"}, // service 2
		{Value: 10, Symbol: mod + "/pkg/b.G", Metric: "alloc", Source: "pyroscope"},
	}
	hots := Rank(samples, nil, mod)
	require.Len(t, hots, 2, "a summed, b")
	require.Equal(t, mod+"/pkg/a.F", hots[0].Symbol)
	require.Equal(t, 75.0, hots[0].WeightPct) // 30/(30+10)
	require.Equal(t, 1, hots[0].Rank)
	require.Equal(t, 25.0, hots[1].WeightPct)
}

func TestRankScopeAndEditable(t *testing.T) {
	samples := []Sample{
		{Value: 50, Symbol: mod + "/pkg/in.F", Metric: "cpu", Source: "pyroscope"},
		{Value: 30, Symbol: mod + "/pkg/out.G", Metric: "cpu", Source: "pyroscope"},
		{Value: 20, Symbol: "runtime.mallocgc", Metric: "cpu", Source: "pyroscope"}, // not editable
	}
	got := map[string]Hotspot{}
	for _, h := range Rank(samples, &Scope{Include: []string{"pkg/in"}}, mod) {
		got[h.Symbol] = h
	}
	in := got[mod+"/pkg/in.F"]
	require.True(t, in.Candidate && in.Editable && in.InScope, "pkg/in.F should be an in-scope candidate")
	out := got[mod+"/pkg/out.G"]
	require.True(t, out.Editable, "pkg/out.G editable")
	require.False(t, out.Candidate, "pkg/out.G is out of scope")
	rt := got["runtime.mallocgc"]
	require.False(t, rt.Editable, "runtime not editable")
	require.False(t, rt.Candidate)
}

func TestInScope(t *testing.T) {
	cases := []struct {
		name string
		sc   *Scope
		pkg  string
		want bool
	}{
		{"nil scope = everything", nil, "pkg/a", true},
		{"include match exact", &Scope{Include: []string{"pkg/a"}}, "pkg/a", true},
		{"include match prefix", &Scope{Include: []string{"pkg/a"}}, "pkg/a/b", true},
		{"include miss", &Scope{Include: []string{"pkg/a"}}, "pkg/b", false},
		{"empty include = all", &Scope{}, "anything", true},
		{"exclude wins over include", &Scope{Include: []string{"pkg"}, Exclude: []string{"pkg/a"}}, "pkg/a", false},
		{"not excluded passes", &Scope{Include: []string{"pkg"}, Exclude: []string{"pkg/a"}}, "pkg/b", true},
		{"trailing /... ignored", &Scope{Include: []string{"pkg/a/..."}}, "pkg/a/b", true},
		{"prefix not substring", &Scope{Include: []string{"pkg/a"}}, "pkg/ab", false},
	}
	for _, c := range cases {
		require.Equal(t, c.want, InScope(c.pkg, c.sc), c.name)
	}
}

func TestResolvePkg(t *testing.T) {
	cases := map[string]string{
		mod + "/pkg/traceql.(*Lexer).Next": "pkg/traceql",
		mod + "/tempodb/encoding.Foo":      "tempodb/encoding",
		"unicode.IsSpace":                  "", // external
		"runtime.mallocgc":                 "", // runtime
		"github.com/other/repo/pkg.Bar":    "", // different module
	}
	for sym, want := range cases {
		require.Equal(t, want, ResolvePkg(sym, mod), "ResolvePkg(%q)", sym)
	}
}

func TestResolvePkgNoModule(t *testing.T) {
	require.Empty(t, ResolvePkg(mod+"/pkg/a.F", ""), "empty modulePath gives empty symbol")
}

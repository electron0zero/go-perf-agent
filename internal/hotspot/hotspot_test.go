package hotspot

import "testing"

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
	if len(hots) != 3 {
		t.Fatalf("got %d hotspots, want 3 (a/alloc, a/inuse, b/alloc)", len(hots))
	}
	// per-metric normalization: alloc total = 40, inuse total = 5
	if h := byKey[sm{mod + "/pkg/a.F", "alloc"}]; h.WeightPct != 25 {
		t.Errorf("a/alloc pct = %v, want 25", h.WeightPct)
	}
	if h := byKey[sm{mod + "/pkg/b.G", "alloc"}]; h.WeightPct != 75 {
		t.Errorf("b/alloc pct = %v, want 75", h.WeightPct)
	}
	if h := byKey[sm{mod + "/pkg/a.F", "inuse"}]; h.WeightPct != 100 {
		t.Errorf("a/inuse pct = %v, want 100 (its own metric)", h.WeightPct)
	}
}

// Rank sums the same symbol across multiple profiles of the same metric (multi-service) by
// absolute value, instead of comparing non-comparable per-file percentages.
func TestRankAggregatesAcrossFiles(t *testing.T) {
	samples := []Sample{
		{Value: 10, Symbol: mod + "/pkg/a.F", Metric: "alloc", Source: "pyroscope"}, // service 1
		{Value: 20, Symbol: mod + "/pkg/a.F", Metric: "alloc", Source: "pyroscope"}, // service 2
		{Value: 10, Symbol: mod + "/pkg/b.G", Metric: "alloc", Source: "pyroscope"},
	}
	hots := Rank(samples, nil, mod)
	if len(hots) != 2 {
		t.Fatalf("got %d hotspots, want 2 (a summed, b)", len(hots))
	}
	if hots[0].Symbol != mod+"/pkg/a.F" || hots[0].WeightPct != 75 || hots[0].Rank != 1 {
		t.Errorf("rank1 = %+v, want a.F at 75%%", hots[0]) // 30/(30+10)
	}
	if hots[1].WeightPct != 25 {
		t.Errorf("rank2 pct = %v, want 25", hots[1].WeightPct)
	}
}

func TestRankScopeAndEditable(t *testing.T) {
	samples := []Sample{
		{Value: 50, Symbol: mod + "/pkg/in.F", Metric: "cpu", Source: "pyroscope"},
		{Value: 30, Symbol: mod + "/pkg/out.G", Metric: "cpu", Source: "pyroscope"},
		{Value: 20, Symbol: "runtime.mallocgc", Metric: "cpu", Source: "pyroscope"}, // not editable
	}
	sc := &Scope{Include: []string{"pkg/in"}}
	hots := Rank(samples, sc, mod)
	got := map[string]Hotspot{}
	for _, h := range hots {
		got[h.Symbol] = h
	}
	if h := got[mod+"/pkg/in.F"]; !h.Candidate || !h.Editable || !h.InScope {
		t.Errorf("pkg/in.F should be an in-scope candidate: %+v", h)
	}
	if h := got[mod+"/pkg/out.G"]; h.Candidate || !h.Editable {
		t.Errorf("pkg/out.G should be editable but out of scope: %+v", h)
	}
	if h := got["runtime.mallocgc"]; h.Editable || h.Candidate {
		t.Errorf("runtime.mallocgc should not be editable: %+v", h)
	}
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
		if got := InScope(c.pkg, c.sc); got != c.want {
			t.Errorf("%s: InScope(%q) = %v, want %v", c.name, c.pkg, got, c.want)
		}
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
		if got := ResolvePkg(sym, mod); got != want {
			t.Errorf("ResolvePkg(%q) = %q, want %q", sym, got, want)
		}
	}
}

func TestResolvePkgNoModule(t *testing.T) {
	if got := ResolvePkg(mod+"/pkg/a.F", ""); got != "" {
		t.Errorf("with empty modulePath, ResolvePkg = %q, want empty", got)
	}
}

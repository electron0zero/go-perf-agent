package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/pprof/profile"
)

func TestLeaderboardMetric(t *testing.T) {
	cases := map[string]string{
		"svc.cpu.leaderboard.json":   "cpu",
		"svc.alloc.leaderboard.json": "alloc",
		"svc.inuse.leaderboard.json": "inuse", // bug fix: inuse is its own metric, not "alloc"
		"weird.leaderboard.json":     "cpu",   // default
		"a/b/svc.inuse.leaderboard.json": "inuse",
	}
	for path, want := range cases {
		if got := leaderboardMetric(path); got != want {
			t.Errorf("leaderboardMetric(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestParsePprof(t *testing.T) {
	// build a real cpu profile: foo flat = 100+50, bar flat = 30; parse it back
	foo := &profile.Function{ID: 1, Name: "github.com/grafana/tempo/pkg/a.foo"}
	bar := &profile.Function{ID: 2, Name: "github.com/grafana/tempo/pkg/a.bar"}
	locFoo := &profile.Location{ID: 1, Line: []profile.Line{{Function: foo}}}
	locBar := &profile.Location{ID: 2, Line: []profile.Line{{Function: bar}}}
	p := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
		Function:   []*profile.Function{foo, bar},
		Location:   []*profile.Location{locFoo, locBar},
		Sample: []*profile.Sample{
			{Location: []*profile.Location{locFoo}, Value: []int64{100}},
			{Location: []*profile.Location{locFoo}, Value: []int64{50}},
			{Location: []*profile.Location{locBar}, Value: []int64{30}},
		},
	}
	path := filepath.Join(t.TempDir(), "x.cpu.prof")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Write(f); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got, err := parsePprof(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{"github.com/grafana/tempo/pkg/a.foo": 150, "github.com/grafana/tempo/pkg/a.bar": 30}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(got), got)
	}
	for _, r := range got {
		if r.metric != "cpu" {
			t.Errorf("%s metric = %q, want cpu", r.symbol, r.metric)
		}
		if r.val != want[r.symbol] {
			t.Errorf("%s val = %v, want %v", r.symbol, r.val, want[r.symbol])
		}
	}

	// topn caps per metric
	if top1, _ := parsePprof(path, 1); len(top1) != 1 || top1[0].symbol != "github.com/grafana/tempo/pkg/a.foo" {
		t.Errorf("topn=1 gave %+v, want just foo (flat 150)", top1)
	}
}

func TestParseLeaderboardAbsoluteValues(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "svc.alloc.leaderboard.json")
	// collect-profiles writes {function,value} rows; a row without a function name is skipped
	writeFile(t, p, `[{"function":"a","value":300},{"function":"b","value":100},{"value":5}]`)

	got := parseLeaderboard(p, "alloc")
	// absolute values are preserved (normalization happens later in rankHotspots), not per-file pct
	want := map[string]float64{"a": 300, "b": 100}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2 (nameless row skipped)", len(got))
	}
	for _, r := range got {
		if r.val != want[r.symbol] {
			t.Errorf("symbol %q val = %v, want %v", r.symbol, r.val, want[r.symbol])
		}
		if r.metric != "alloc" {
			t.Errorf("symbol %q metric = %q, want alloc", r.symbol, r.metric)
		}
	}
}

// rankHotspots: a symbol hot in two metrics keeps a row for EACH (regression test for the
// cross-metric blend bug that silently dropped the lower-pct metric).
func TestRankHotspotsKeepsBothMetrics(t *testing.T) {
	defer setModulePath("github.com/grafana/tempo")()
	raws := []rawHot{
		{val: 10, symbol: "github.com/grafana/tempo/pkg/a.F", metric: "alloc", source: "pyroscope"},
		{val: 5, symbol: "github.com/grafana/tempo/pkg/a.F", metric: "inuse", source: "pyroscope"},
		{val: 30, symbol: "github.com/grafana/tempo/pkg/b.G", metric: "alloc", source: "pyroscope"},
	}
	hots := rankHotspots(raws, nil)

	type sm struct{ symbol, metric string }
	byKey := map[sm]Hotspot{}
	for _, h := range hots {
		byKey[sm{h.Symbol, h.Metric}] = h
	}
	if len(hots) != 3 {
		t.Fatalf("got %d hotspots, want 3 (a/alloc, a/inuse, b/alloc)", len(hots))
	}
	// per-metric normalization: alloc total = 40, inuse total = 5
	if h := byKey[sm{"github.com/grafana/tempo/pkg/a.F", "alloc"}]; h.WeightPct != 25 {
		t.Errorf("a/alloc pct = %v, want 25", h.WeightPct)
	}
	if h := byKey[sm{"github.com/grafana/tempo/pkg/b.G", "alloc"}]; h.WeightPct != 75 {
		t.Errorf("b/alloc pct = %v, want 75", h.WeightPct)
	}
	if h := byKey[sm{"github.com/grafana/tempo/pkg/a.F", "inuse"}]; h.WeightPct != 100 {
		t.Errorf("a/inuse pct = %v, want 100 (its own metric)", h.WeightPct)
	}
}

// rankHotspots sums the same symbol across multiple profiles of the same metric (multi-service)
// by absolute value, instead of comparing non-comparable per-file percentages.
func TestRankHotspotsAggregatesAcrossFiles(t *testing.T) {
	defer setModulePath("github.com/grafana/tempo")()
	raws := []rawHot{
		{val: 10, symbol: "github.com/grafana/tempo/pkg/a.F", metric: "alloc", source: "pyroscope"}, // service 1
		{val: 20, symbol: "github.com/grafana/tempo/pkg/a.F", metric: "alloc", source: "pyroscope"}, // service 2
		{val: 10, symbol: "github.com/grafana/tempo/pkg/b.G", metric: "alloc", source: "pyroscope"},
	}
	hots := rankHotspots(raws, nil)
	if len(hots) != 2 {
		t.Fatalf("got %d hotspots, want 2 (a summed, b)", len(hots))
	}
	if hots[0].Symbol != "github.com/grafana/tempo/pkg/a.F" || hots[0].WeightPct != 75 || hots[0].Rank != 1 {
		t.Errorf("rank1 = %+v, want a.F at 75%%", hots[0]) // 30/(30+10)
	}
	if hots[1].WeightPct != 25 {
		t.Errorf("rank2 pct = %v, want 25", hots[1].WeightPct)
	}
}

// gatherHotspots end-to-end: reads leaderboard files from gpaDir, labels metrics from filenames,
// keeps a symbol per metric, and tags editability. Guards the inuse-vs-alloc + blend bugs.
func TestGatherHotspots(t *testing.T) {
	defer setModulePath("github.com/grafana/tempo")()
	dir := t.TempDir()
	old := gpaDir
	gpaDir = dir
	defer func() { gpaDir = old }()
	if err := os.MkdirAll(filepath.Join(dir, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "profiles", "svc.cpu.leaderboard.json"),
		`[{"function":"github.com/grafana/tempo/pkg/a.F","value":100},{"function":"runtime.x","value":50}]`)
	writeFile(t, filepath.Join(dir, "profiles", "svc.inuse.leaderboard.json"),
		`[{"function":"github.com/grafana/tempo/pkg/a.F","value":10}]`)

	hots, err := gatherHotspots("", 20)
	if err != nil {
		t.Fatal(err)
	}
	type sm struct{ symbol, metric string }
	got := map[sm]Hotspot{}
	for _, h := range hots {
		got[sm{h.Symbol, h.Metric}] = h
	}
	if len(hots) != 3 {
		t.Fatalf("got %d hotspots, want 3 (F/cpu, runtime.x/cpu, F/inuse): %+v", len(hots), hots)
	}
	if h := got[sm{"github.com/grafana/tempo/pkg/a.F", "cpu"}]; h.WeightPct != 66.6667 || !h.Editable {
		t.Errorf("F/cpu = %+v, want 66.6667%% editable", h)
	}
	if h := got[sm{"runtime.x", "cpu"}]; h.Editable {
		t.Errorf("runtime.x should not be editable: %+v", h)
	}
	if h := got[sm{"github.com/grafana/tempo/pkg/a.F", "inuse"}]; h.WeightPct != 100 {
		t.Errorf("F/inuse = %+v, want 100%% (own metric)", h)
	}
}

func TestRankHotspotsScopeAndEditable(t *testing.T) {
	defer setModulePath("github.com/grafana/tempo")()
	raws := []rawHot{
		{val: 50, symbol: "github.com/grafana/tempo/pkg/in.F", metric: "cpu", source: "pyroscope"},
		{val: 30, symbol: "github.com/grafana/tempo/pkg/out.G", metric: "cpu", source: "pyroscope"},
		{val: 20, symbol: "runtime.mallocgc", metric: "cpu", source: "pyroscope"}, // not editable
	}
	sc := &Scope{Include: []string{"pkg/in"}}
	hots := rankHotspots(raws, sc)
	got := map[string]Hotspot{}
	for _, h := range hots {
		got[h.Symbol] = h
	}
	if h := got["github.com/grafana/tempo/pkg/in.F"]; !h.Candidate || !h.Editable || !h.InScope {
		t.Errorf("pkg/in.F should be an in-scope candidate: %+v", h)
	}
	if h := got["github.com/grafana/tempo/pkg/out.G"]; h.Candidate || !h.Editable {
		t.Errorf("pkg/out.G should be editable but out of scope: %+v", h)
	}
	if h := got["runtime.mallocgc"]; h.Editable || h.Candidate {
		t.Errorf("runtime.mallocgc should not be editable: %+v", h)
	}
}

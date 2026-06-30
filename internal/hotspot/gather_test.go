package hotspot

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/pprof/profile"
)

func TestParseProfile(t *testing.T) {
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

	got, err := parseProfile(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{"github.com/grafana/tempo/pkg/a.foo": 150, "github.com/grafana/tempo/pkg/a.bar": 30}
	if len(got) != 2 {
		t.Fatalf("got %d samples, want 2: %+v", len(got), got)
	}
	for _, r := range got {
		if r.Metric != "cpu" {
			t.Errorf("%s metric = %q, want cpu", r.Symbol, r.Metric)
		}
		if r.Value != want[r.Symbol] {
			t.Errorf("%s val = %v, want %v", r.Symbol, r.Value, want[r.Symbol])
		}
	}

	// topn caps per metric
	if top1, _ := parseProfile(path, 1); len(top1) != 1 || top1[0].Symbol != "github.com/grafana/tempo/pkg/a.foo" {
		t.Errorf("topn=1 gave %+v, want just foo (flat 150)", top1)
	}
}

// inuse is ~zero at the end of a benchmark, so a local profile's inuse_space is meaningless:
// parseProfile must skip inuse for local.* files but keep it for gcx-collected (pyroscope) ones.
func TestParseProfileInuseLocalOnly(t *testing.T) {
	dir := t.TempDir()
	flat := map[string]int64{"github.com/grafana/tempo/pkg/a.F": 100}

	local := filepath.Join(dir, "local.mem.pb.gz")
	writePprof(t, local, "inuse_space", "bytes", flat)
	if got, _ := parseProfile(local, 0); len(got) != 0 {
		t.Errorf("local inuse should be skipped, got %+v", got)
	}

	prod := filepath.Join(dir, "svc.inuse.pb.gz")
	writePprof(t, prod, "inuse_space", "bytes", flat)
	got, _ := parseProfile(prod, 0)
	if len(got) != 1 || got[0].Metric != "inuse" || got[0].Source != "pyroscope" {
		t.Errorf("pyroscope inuse should be kept, got %+v", got)
	}
}

// Gather end-to-end: parses gcx-collected pprof files from dir/profiles, keeps a symbol per metric,
// and tags editability. Guards the inuse-vs-alloc + cross-metric blend bugs.
func TestGather(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePprof(t, filepath.Join(dir, "profiles", "svc.cpu.pb.gz"), "cpu", "nanoseconds",
		map[string]int64{"github.com/grafana/tempo/pkg/a.F": 100, "runtime.x": 50})
	writePprof(t, filepath.Join(dir, "profiles", "svc.inuse.pb.gz"), "inuse_space", "bytes",
		map[string]int64{"github.com/grafana/tempo/pkg/a.F": 10})

	hots, err := Gather(dir, "", 20, "github.com/grafana/tempo", nil)
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

func TestSplitCSV(t *testing.T) {
	if got := SplitCSV("a, b ,,c"); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("SplitCSV = %v", got)
	}
	if got := SplitCSV(""); got != nil {
		t.Errorf("SplitCSV(\"\") = %v, want nil", got)
	}
}

// writePprof writes a single-sample-type pprof to path: one {function: flat-value} pair per entry.
func writePprof(t *testing.T, path, sampleType, unit string, flat map[string]int64) {
	t.Helper()
	p := &profile.Profile{SampleType: []*profile.ValueType{{Type: sampleType, Unit: unit}}}
	var id uint64
	for name, v := range flat {
		id++
		fn := &profile.Function{ID: id, Name: name}
		loc := &profile.Location{ID: id, Line: []profile.Line{{Function: fn}}}
		p.Function = append(p.Function, fn)
		p.Location = append(p.Location, loc)
		p.Sample = append(p.Sample, &profile.Sample{Location: []*profile.Location{loc}, Value: []int64{v}})
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := p.Write(f); err != nil {
		t.Fatal(err)
	}
}

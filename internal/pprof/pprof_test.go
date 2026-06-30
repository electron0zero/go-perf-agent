package pprof

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/pprof/profile"
)

// writeProf writes a single-sample-type pprof to path: one {function: flat-value} pair per entry.
func writeProf(t *testing.T, path, sampleType, unit string, flat map[string]int64) {
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

func TestParseFlat(t *testing.T) {
	dir := t.TempDir()
	cpu := filepath.Join(dir, "x.cpu.pb.gz")
	writeProf(t, cpu, "cpu", "nanoseconds", map[string]int64{"a.Foo": 100, "a.Bar": 30, "a.Baz": 10})

	all, err := ParseFlat(cpu, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d weights, want 3: %+v", len(all), all)
	}
	for _, w := range all {
		if w.Metric != "cpu" {
			t.Errorf("%s metric = %q, want cpu", w.Func, w.Metric)
		}
	}

	// topn caps per metric, keeping the heaviest first
	top2, _ := ParseFlat(cpu, 2, false)
	if len(top2) != 2 || top2[0].Func != "a.Foo" || top2[0].Value != 100 {
		t.Errorf("top2 = %+v, want Foo(100) first then Bar(30)", top2)
	}

	// dropInuse skips inuse_space (meaningless from a local benchmark); kept otherwise
	inuse := filepath.Join(dir, "x.inuse.pb.gz")
	writeProf(t, inuse, "inuse_space", "bytes", map[string]int64{"a.F": 100})
	if got, _ := ParseFlat(inuse, 0, true); len(got) != 0 {
		t.Errorf("dropInuse should skip inuse, got %+v", got)
	}
	if got, _ := ParseFlat(inuse, 0, false); len(got) != 1 || got[0].Metric != "inuse" {
		t.Errorf("inuse should be kept when not dropped, got %+v", got)
	}
}

func TestVersion(t *testing.T) {
	dir := t.TempDir()
	m := &profile.Mapping{ID: 1, BuildID: "abc123", HasFunctions: true}
	fn := &profile.Function{ID: 1, Name: "a.F"}
	loc := &profile.Location{ID: 1, Mapping: m, Line: []profile.Line{{Function: fn}}}
	p := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
		Mapping:    []*profile.Mapping{m},
		Function:   []*profile.Function{fn},
		Location:   []*profile.Location{loc},
		Sample:     []*profile.Sample{{Location: []*profile.Location{loc}, Value: []int64{1}}},
	}
	path := filepath.Join(dir, "v.pb.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Write(f); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if got := Version(path); got != "abc123" {
		t.Errorf("Version = %q, want abc123 (mapping build id)", got)
	}
	if got := Version(filepath.Join(dir, "missing.pb.gz")); got != "" {
		t.Errorf("Version(missing) = %q, want empty", got)
	}
}

package hotspot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/require"
)

func TestParseProfile(t *testing.T) {
	// build a real cpu profile: foo flat = 100+50, bar flat = 30 - parse it back
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
	require.NoError(t, err)
	require.NoError(t, p.Write(f))
	f.Close()

	got, err := parseProfile(path, 0)
	require.NoError(t, err)
	require.Len(t, got, 2)
	want := map[string]float64{"github.com/grafana/tempo/pkg/a.foo": 150, "github.com/grafana/tempo/pkg/a.bar": 30}
	for _, r := range got {
		require.Equal(t, "cpu", r.Metric, r.Symbol)
		require.Equal(t, want[r.Symbol], r.Value, r.Symbol)
	}

	// topn caps per metric, heaviest first
	top1, _ := parseProfile(path, 1)
	require.Len(t, top1, 1)
	require.Equal(t, "github.com/grafana/tempo/pkg/a.foo", top1[0].Symbol, "flat 150 is heaviest")
}

// inuse is ~zero at the end of a benchmark, so a local profile's inuse_space is meaningless:
// parseProfile must skip inuse for local.* files but keep it for gcx-collected (pyroscope) ones.
func TestParseProfileInuseLocalOnly(t *testing.T) {
	dir := t.TempDir()
	flat := map[string]int64{"github.com/grafana/tempo/pkg/a.F": 100}

	local := filepath.Join(dir, "local.mem.pb.gz")
	writePprof(t, local, "inuse_space", "bytes", flat)
	got, _ := parseProfile(local, 0)
	require.Empty(t, got, "local inuse should be skipped")

	prod := filepath.Join(dir, "svc.inuse.pb.gz")
	writePprof(t, prod, "inuse_space", "bytes", flat)
	got, _ = parseProfile(prod, 0)
	require.Len(t, got, 1, "pyroscope inuse should be kept")
	require.Equal(t, "inuse", got[0].Metric)
	require.Equal(t, "pyroscope", got[0].Source)
}

// Gather end-to-end: parses gcx-collected pprof files from dir/profiles, keeps a symbol per metric,
// and tags editability. Guards the inuse-vs-alloc + cross-metric blend bugs.
func TestGather(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "profiles"), 0o755))
	writePprof(t, filepath.Join(dir, "profiles", "svc.cpu.pb.gz"), "cpu", "nanoseconds",
		map[string]int64{"github.com/grafana/tempo/pkg/a.F": 100, "runtime.x": 50})
	writePprof(t, filepath.Join(dir, "profiles", "svc.inuse.pb.gz"), "inuse_space", "bytes",
		map[string]int64{"github.com/grafana/tempo/pkg/a.F": 10})

	hots, err := Gather(dir, "", 20, "github.com/grafana/tempo", nil)
	require.NoError(t, err)

	type sm struct{ symbol, metric string }
	got := map[sm]Hotspot{}
	for _, h := range hots {
		got[sm{h.Symbol, h.Metric}] = h
	}
	require.Len(t, hots, 3, "F/cpu, runtime.x/cpu, F/inuse")
	fcpu := got[sm{"github.com/grafana/tempo/pkg/a.F", "cpu"}]
	require.Equal(t, 66.6667, fcpu.WeightPct)
	require.True(t, fcpu.Editable)
	require.False(t, got[sm{"runtime.x", "cpu"}].Editable, "runtime.x not editable")
	require.Equal(t, 100.0, got[sm{"github.com/grafana/tempo/pkg/a.F", "inuse"}].WeightPct, "own metric")
}

func TestSplitCSV(t *testing.T) {
	require.Equal(t, []string{"a", "b", "c"}, SplitCSV("a, b ,,c"))
	require.Nil(t, SplitCSV(""))
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
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, p.Write(f))
}

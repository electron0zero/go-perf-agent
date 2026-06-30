package pprof

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, p.Write(f))
}

func TestParseFlat(t *testing.T) {
	dir := t.TempDir()
	cpu := filepath.Join(dir, "x.cpu.pb.gz")
	writeProf(t, cpu, "cpu", "nanoseconds", map[string]int64{"a.Foo": 100, "a.Bar": 30, "a.Baz": 10})

	all, err := ParseFlat(cpu, 0, false)
	require.NoError(t, err)
	require.Len(t, all, 3)
	for _, w := range all {
		require.Equal(t, "cpu", w.Metric, w.Func)
	}

	// topn caps per metric, keeping the heaviest first
	top2, _ := ParseFlat(cpu, 2, false)
	require.Len(t, top2, 2)
	require.Equal(t, "a.Foo", top2[0].Func, "heaviest first")
	require.Equal(t, 100.0, top2[0].Value)

	// dropInuse skips inuse_space (meaningless from a local benchmark); kept otherwise
	inuse := filepath.Join(dir, "x.inuse.pb.gz")
	writeProf(t, inuse, "inuse_space", "bytes", map[string]int64{"a.F": 100})
	got, _ := ParseFlat(inuse, 0, true)
	require.Empty(t, got, "dropInuse should skip inuse")
	got, _ = ParseFlat(inuse, 0, false)
	require.Len(t, got, 1, "inuse kept when not dropped")
	require.Equal(t, "inuse", got[0].Metric)
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
	require.NoError(t, err)
	require.NoError(t, p.Write(f))
	f.Close()

	require.Equal(t, "abc123", Version(path), "mapping build id")
	require.Empty(t, Version(filepath.Join(dir, "missing.pb.gz")), "missing file")
}

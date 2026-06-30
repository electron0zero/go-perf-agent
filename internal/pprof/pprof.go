// Package pprof parses Go pprof profiles into per-function flat self weights (the hotspot signal).
package pprof

import (
	"os"
	"sort"
	"strings"

	"github.com/google/pprof/profile"
)

// FuncWeight is one function's flat self weight in a single metric (cpu | alloc | inuse).
type FuncWeight struct {
	Func   string
	Metric string
	Value  float64
}

// ParseFlat reads a pprof file (gzip handled) and returns the top-N functions by flat self weight
// per metric: cpu, alloc_space (alloc), inuse_space (inuse). Flat self = the leaf frame of each
// sample, summed per function. dropInuse skips inuse (it is ~zero at the end of a local benchmark,
// so meaningless there - a production-only signal).
func ParseFlat(path string, topn int, dropInuse bool) ([]FuncWeight, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	p, err := profile.Parse(f)
	if err != nil {
		return nil, err
	}
	want := map[int]string{} // sample-type index -> our metric name
	sawKnown := false
	for i, st := range p.SampleType {
		switch st.Type {
		case "cpu":
			want[i] = "cpu"
			sawKnown = true
		case "alloc_space":
			want[i] = "alloc"
			sawKnown = true
		case "inuse_space":
			sawKnown = true
			if !dropInuse {
				want[i] = "inuse"
			}
		}
	}
	if !sawKnown && len(p.SampleType) > 0 {
		want[len(p.SampleType)-1] = "cpu" // unknown profile: rank by its last sample type
	}

	type key struct{ fn, metric string }
	flat := map[key]float64{}
	for _, s := range p.Sample {
		if len(s.Location) == 0 || len(s.Location[0].Line) == 0 {
			continue
		}
		fn := s.Location[0].Line[0].Function // innermost (leaf) frame = self
		if fn == nil {
			continue
		}
		for i, m := range want {
			if i < len(s.Value) && s.Value[i] != 0 {
				flat[key{fn.Name, m}] += float64(s.Value[i])
			}
		}
	}

	byMetric := map[string][]FuncWeight{}
	for k, v := range flat {
		if v <= 0 {
			continue
		}
		byMetric[k.metric] = append(byMetric[k.metric], FuncWeight{Func: k.fn, Metric: k.metric, Value: v})
	}
	var out []FuncWeight
	for _, ws := range byMetric {
		sort.SliceStable(ws, func(i, j int) bool { return ws[i].Value > ws[j].Value })
		if topn > 0 && len(ws) > topn {
			ws = ws[:topn]
		}
		out = append(out, ws...)
	}
	return out, nil
}

// Version returns the build id baked into a pprof (mapping BuildID, else a version/git comment),
// so the caller can validate against the deployed source ref. Empty if none.
func Version(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	p, err := profile.Parse(f)
	if err != nil {
		return ""
	}
	for _, m := range p.Mapping {
		if m.BuildID != "" {
			return m.BuildID
		}
	}
	for _, c := range p.Comments {
		if strings.Contains(strings.ToLower(c), "git") || strings.Contains(strings.ToLower(c), "version") {
			return c
		}
	}
	return ""
}

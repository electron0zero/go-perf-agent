package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/google/pprof/profile"
)

type rawHot struct {
	val    float64 // raw self weight (flat self) summed from the profile's leaf frames
	symbol string
	metric string
	source string
}

type hotspotsCmd struct {
	Pprof string `help:"Explicit pprof file (else glob .go-perf-agent/profiles/)"`
	Top   int    `default:"20" help:"Top N nodes per profile"`
}

func (c *hotspotsCmd) Run() error { return runHotspots(c.Pprof, c.Top) }

func runHotspots(pprof string, topn int) error {
	hots, err := gatherHotspots(pprof, topn)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(gpaDir, "hotspots.json"), hots); err != nil {
		return err
	}
	ncand := 0
	for _, h := range hots {
		if h.Candidate {
			ncand++
		}
	}
	info("wrote %s/hotspots.json (%d symbols, %d in-scope candidates)", gpaDir, len(hots), ncand)
	shown := 0
	for _, h := range hots {
		if h.Candidate && shown < 15 {
			fmt.Fprintf(os.Stderr, "  %d. %.2f%% [%s] %s\n", h.Rank, h.WeightPct, h.Metric, h.Symbol)
			shown++
		}
	}
	return nil
}

// gatherHotspots parses profiles into a ranked, scope-tagged hotspot list (no file writes).
// Reused by target-diff for its optional profile-weight overlay.
func gatherHotspots(pprof string, topn int) ([]Hotspot, error) {
	ensureDirs()

	var raws []rawHot

	// pprof files: gcx-collected (.pb.gz) and local go-test profiles (.prof) parse the same way.
	var profs []string
	if pprof != "" {
		profs = []string{pprof}
	} else {
		for _, g := range []string{"*.pb.gz", "*.prof"} {
			m, _ := filepath.Glob(filepath.Join(gpaDir, "profiles", g))
			profs = append(profs, m...)
		}
	}
	for _, p := range profs {
		if !fileExists(p) {
			continue
		}
		info("parsing pprof %s", p)
		rs, err := parsePprof(p, topn)
		if err != nil {
			info("  pprof parse failed for %s: %v (skipping)", p, err)
			continue
		}
		raws = append(raws, rs...)
	}

	if len(raws) == 0 {
		return nil, fmt.Errorf("no profiles found. Run collect-profiles/collect-local, pass --pprof FILE, or run selftest")
	}

	sc := loadScope()
	if sc != nil {
		info("scope: include=%v exclude=%v", sc.Include, sc.Exclude)
	}
	return rankHotspots(raws, sc), nil
}

// rankHotspots aggregates raw self weights by (symbol, metric) across every profile, then
// normalizes within each metric. Per-metric normalization is the point: cpu%, alloc% and inuse%
// are only comparable inside their own metric, and a symbol that is hot in two metrics keeps a
// row for each instead of one silently dropping the other.
func rankHotspots(raws []rawHot, sc *Scope) []Hotspot {
	type key struct{ symbol, metric string }
	sum := map[key]float64{}
	src := map[key]string{}
	var order []key
	for _, r := range raws {
		k := key{r.symbol, r.metric}
		if _, seen := sum[k]; !seen {
			order = append(order, k)
			src[k] = r.source
		}
		sum[k] += r.val
	}
	metricTotal := map[string]float64{}
	for k, v := range sum {
		metricTotal[k.metric] += v
	}

	hots := make([]Hotspot, 0, len(order))
	for _, k := range order {
		pct := 0.0
		if t := metricTotal[k.metric]; t > 0 {
			pct = sum[k] / t * 100
		}
		pkg := resolvePkg(k.symbol)
		editable := pkg != ""
		scoped := editable && inScope(pkg, sc)
		hots = append(hots, Hotspot{
			Symbol: k.symbol, Package: pkg, WeightPct: round4(pct),
			Metric: k.metric, Source: src[k],
			Editable: editable, InScope: scoped, Candidate: editable && scoped,
		})
	}
	// stable order (seed = first-seen) so equal weights rank deterministically
	sort.SliceStable(hots, func(i, j int) bool { return hots[i].WeightPct > hots[j].WeightPct })
	for i := range hots {
		hots[i].Rank = i + 1
	}
	return hots
}

// parsePprof reads a pprof profile with the canonical profile package (handles gzip) and returns
// per-function flat self weight for each metric the profile carries: cpu, alloc_space (alloc),
// inuse_space (inuse). Flat self = the leaf frame of each sample, summed per function. Keeps the
// top `topn` functions per metric. Replaces scraping `go tool pprof -top` text output.
func parsePprof(path string, topn int) ([]rawHot, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	p, err := profile.Parse(f)
	if err != nil {
		return nil, err
	}
	// local profiles are written as local.*.prof; gcx-collected ones come from pyroscope.
	local := strings.HasPrefix(filepath.Base(path), "local.")
	// which sample-type index maps to which of our metrics
	want := map[int]string{}
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
			// inuse (resident heap) is ~zero at the end of a benchmark, so it is meaningless from
			// a local profile; it is a production-only signal (pyroscope/live heap).
			sawKnown = true
			if !local {
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

	source := "pyroscope"
	if local {
		source = "local-pprof"
	}
	byMetric := map[string][]rawHot{}
	for k, v := range flat {
		if v <= 0 {
			continue
		}
		byMetric[k.metric] = append(byMetric[k.metric], rawHot{val: v, symbol: k.fn, metric: k.metric, source: source})
	}
	var hs []rawHot
	for _, rs := range byMetric {
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].val > rs[j].val })
		if topn > 0 && len(rs) > topn {
			rs = rs[:topn]
		}
		hs = append(hs, rs...)
	}
	return hs, nil
}

func round4(f float64) float64 {
	s := strconv.FormatFloat(f, 'f', 4, 64)
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

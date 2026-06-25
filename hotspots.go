package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/google/pprof/profile"
)

type rawHot struct {
	val    float64 // raw self weight: absolute (pyroscope leaderboard) or flat% (local pprof)
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

	// source 1: pprof files (cpu/alloc) via `go tool pprof -top`
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

	// source 2: pyroscope leaderboards (json)
	lbs, _ := filepath.Glob(filepath.Join(gpaDir, "profiles", "*.leaderboard.json"))
	for _, lb := range lbs {
		raws = append(raws, parseLeaderboard(lb, leaderboardMetric(lb))...)
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

// leaderboardMetric derives the metric from the leaderboard filename. inuse (resident heap) is
// its own metric, distinct from alloc (churn) - they rank differently and must not be blended.
func leaderboardMetric(path string) string {
	switch {
	case strings.Contains(path, ".alloc."):
		return "alloc"
	case strings.Contains(path, ".inuse."):
		return "inuse"
	default:
		return "cpu"
	}
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
	// which sample-type index maps to which of our metrics
	want := map[int]string{}
	for i, st := range p.SampleType {
		switch st.Type {
		case "cpu":
			want[i] = "cpu"
		case "alloc_space":
			want[i] = "alloc"
		case "inuse_space":
			want[i] = "inuse"
		}
	}
	if len(want) == 0 && len(p.SampleType) > 0 {
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

	byMetric := map[string][]rawHot{}
	for k, v := range flat {
		if v <= 0 {
			continue
		}
		byMetric[k.metric] = append(byMetric[k.metric], rawHot{val: v, symbol: k.fn, metric: k.metric, source: "local-pprof"})
	}
	var hs []rawHot
	for _, rs := range byMetric {
		sort.Slice(rs, func(i, j int) bool { return rs[i].val > rs[j].val })
		if topn > 0 && len(rs) > topn {
			rs = rs[:topn]
		}
		hs = append(hs, rs...)
	}
	return hs, nil
}

// parseLeaderboard reads a leaderboard file written by collect-profiles: a JSON array of
// {function, value} rows where value is absolute self weight. rankHotspots normalizes per metric.
func parseLeaderboard(path, kind string) []rawHot {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var rows []leaderboardRow
	if json.Unmarshal(b, &rows) != nil {
		return nil
	}
	var hs []rawHot
	for _, r := range rows {
		if r.Function == "" {
			continue
		}
		hs = append(hs, rawHot{val: r.Value, symbol: r.Function, metric: kind, source: "pyroscope"})
	}
	return hs
}

func round4(f float64) float64 {
	s := strconv.FormatFloat(f, 'f', 4, 64)
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

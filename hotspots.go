package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type rawHot struct {
	pct    float64
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
		// a mem/alloc profile is ranked by allocations, not cpu time
		kind := "cpu"
		ppArgs := []string{"tool", "pprof", "-top", "-flat", "-nodecount=" + strconv.Itoa(topn)}
		if strings.Contains(p, "mem") || strings.Contains(p, "alloc") {
			kind = "alloc"
			ppArgs = append(ppArgs, "-sample_index=alloc_space")
		}
		info("parsing pprof %s (%s)", p, kind)
		out, _, err := run("", "go", append(ppArgs, p)...)
		if err != nil {
			info("  pprof failed for %s (skipping)", p)
			continue
		}
		raws = append(raws, parsePprofTop(out, kind)...)
	}

	// source 2: pyroscope leaderboards (json)
	lbs, _ := filepath.Glob(filepath.Join(gpaDir, "profiles", "*.leaderboard.json"))
	for _, lb := range lbs {
		kind := "cpu"
		if strings.Contains(lb, ".alloc.") {
			kind = "alloc"
		}
		info("parsing pyroscope leaderboard %s (%s)", lb, kind)
		raws = append(raws, parseLeaderboard(lb, kind)...)
	}

	if len(raws) == 0 {
		return nil, fmt.Errorf("no profiles found. Run collect-profiles/collect-local, pass --pprof FILE, or run selftest")
	}

	// rank desc by weight, dedup by symbol (keep highest)
	sort.SliceStable(raws, func(i, j int) bool { return raws[i].pct > raws[j].pct })
	seen := map[string]bool{}
	sc := loadScope()
	if sc != nil {
		info("scope: include=%v exclude=%v", sc.Include, sc.Exclude)
	}

	var hots []Hotspot
	rank := 0
	for _, r := range raws {
		if seen[r.symbol] {
			continue
		}
		seen[r.symbol] = true
		pkg := resolvePkg(r.symbol)
		editable := pkg != ""
		scoped := editable && inScope(pkg, sc)
		rank++
		hots = append(hots, Hotspot{
			Rank: rank, Symbol: r.symbol, Package: pkg, WeightPct: round4(r.pct),
			Metric: r.metric, Source: r.source,
			Editable: editable, InScope: scoped, Candidate: editable && scoped,
		})
	}
	return hots, nil
}

// parsePprofTop reads `go tool pprof -top -flat` text. Data lines look like:
//
//	4030ms 53.81% 53.81%     5910ms 78.91%  github.com/grafana/tempo/pkg/traceql.isAttributeRune
//
// We take flat% (field 1, ends with %) as the weight and the trailing fields as the symbol.
func parsePprofTop(out, kind string) []rawHot {
	var hs []rawHot
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 6 || !strings.HasSuffix(f[1], "%") {
			continue
		}
		pct, err := strconv.ParseFloat(strings.TrimSuffix(f[1], "%"), 64)
		if err != nil {
			continue
		}
		hs = append(hs, rawHot{pct: pct, symbol: strings.Join(f[5:], " "), metric: kind, source: "local-pprof"})
	}
	return hs
}

// parseLeaderboard reads a pyroscope SelectSeries --top JSON leaderboard. Shapes vary across
// gcx versions, so we stay lenient: an array (or {data|series:[...]}) of rows each carrying a
// name (name|function|labels) and a value (value|total). Weight = value / sum * 100.
func parseLeaderboard(path, kind string) []rawHot {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal(b, &rows) != nil {
		var wrap struct {
			Data   []map[string]json.RawMessage `json:"data"`
			Series []map[string]json.RawMessage `json:"series"`
		}
		if json.Unmarshal(b, &wrap) != nil {
			return nil
		}
		rows = wrap.Data
		if rows == nil {
			rows = wrap.Series
		}
	}
	type nv struct {
		name string
		val  float64
	}
	var nvs []nv
	var total float64
	for _, row := range rows {
		name := firstString(row, "name", "function", "labels")
		val := firstNumber(row, "value", "total")
		if name == "" {
			continue
		}
		nvs = append(nvs, nv{name, val})
		total += val
	}
	var hs []rawHot
	for _, x := range nvs {
		pct := 0.0
		if total > 0 {
			pct = x.val / total * 100
		}
		hs = append(hs, rawHot{pct: pct, symbol: x.name, metric: kind, source: "pyroscope"})
	}
	return hs
}

func firstString(m map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		if raw, ok := m[k]; ok {
			var s string
			if json.Unmarshal(raw, &s) == nil && s != "" {
				return s
			}
			return strings.Trim(string(raw), `"`)
		}
	}
	return ""
}

func firstNumber(m map[string]json.RawMessage, keys ...string) float64 {
	for _, k := range keys {
		if raw, ok := m[k]; ok {
			var f float64
			if json.Unmarshal(raw, &f) == nil {
				return f
			}
		}
	}
	return 0
}

func round4(f float64) float64 {
	s := strconv.FormatFloat(f, 'f', 4, 64)
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

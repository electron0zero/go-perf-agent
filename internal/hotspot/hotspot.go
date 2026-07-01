// Package hotspot ranks profiled self-weight samples into scope-tagged hotspots, and resolves
// pprof symbols to repo-relative packages so the loop knows what is editable and in scope.
package hotspot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Scope: which parts of the codebase are in/out of bounds. Entries are path prefixes relative
// to the module root (e.g. "pkg/parquet", "tempodb"); a trailing "/..." is ignored.
type Scope struct {
	Root    string   `json:"root"`
	Include []string `json:"include"`
	Exclude []string `json:"exclude"`
}

// Hotspot: one ranked hot symbol. candidate = editable (ours, not stdlib/vendor) AND in_scope.
type Hotspot struct {
	Rank      int     `json:"rank"`
	Symbol    string  `json:"symbol"`
	Package   string  `json:"package"`
	WeightPct float64 `json:"weight_pct"`
	Metric    string  `json:"metric"`
	Source    string  `json:"source"`
	Editable  bool    `json:"editable"`
	InScope   bool    `json:"in_scope"`
	Candidate bool    `json:"candidate"`
}

// Sample is one function's flat self weight in a metric, the raw input to Rank.
type Sample struct {
	Symbol string
	Metric string
	Source string
	Value  float64
}

// Rank aggregates raw self weights by (symbol, metric) across every profile, then
// normalizes within each metric. Per-metric normalization is the point: cpu%, alloc% and inuse%
// are only comparable inside their own metric, and a symbol that is hot in two metrics keeps a
// row for each instead of one silently dropping the other.
func Rank(samples []Sample, sc *Scope, modulePath string) []Hotspot {
	type key struct{ symbol, metric string }
	sum := map[key]float64{}
	src := map[key]string{}
	var order []key
	for _, r := range samples {
		k := key{r.Symbol, r.Metric}
		if _, seen := sum[k]; !seen {
			order = append(order, k)
			src[k] = r.Source
		}
		sum[k] += r.Value
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
		pkg := ResolvePkg(k.symbol, modulePath)
		editable := pkg != ""
		scoped := editable && InScope(pkg, sc)
		hots = append(hots, Hotspot{
			Symbol: k.symbol, Package: pkg, WeightPct: round4(pct),
			Metric: k.metric, Source: src[k],
			Editable: editable, InScope: scoped, Candidate: editable && scoped,
		})
	}
	// symbol/metric tiebreak so equal weights rank reproducibly, independent of profile input order
	sort.Slice(hots, func(i, j int) bool {
		if hots[i].WeightPct != hots[j].WeightPct {
			return hots[i].WeightPct > hots[j].WeightPct
		}
		if hots[i].Symbol != hots[j].Symbol {
			return hots[i].Symbol < hots[j].Symbol
		}
		return hots[i].Metric < hots[j].Metric
	})
	for i := range hots {
		hots[i].Rank = i + 1
	}
	return hots
}

// InScope: a package is in scope iff (include empty OR matches an include) AND matches no
// exclude. Exclude wins. Matching is path-prefix, with a trailing "/..." or "/" ignored.
func InScope(pkg string, sc *Scope) bool {
	if sc == nil {
		return true
	}
	norm := func(e string) string { return strings.TrimSuffix(strings.TrimSuffix(e, "/..."), "/") }
	matches := func(list []string) bool {
		for _, e := range list {
			e = norm(e)
			if pkg == e || strings.HasPrefix(pkg, e+"/") {
				return true
			}
		}
		return false
	}
	if matches(sc.Exclude) {
		return false
	}
	if len(sc.Include) == 0 {
		return true
	}
	return matches(sc.Include)
}

// ResolvePkg maps a pprof/pyroscope symbol to a repo-relative package dir, or "" if it is not
// ours (stdlib / vendor / runtime), which means not editable.
//
//	github.com/grafana/tempo/pkg/traceql.(*Lexer).Next -> pkg/traceql
//	unicode.IsSpace                                     -> "" (external)
func ResolvePkg(sym, modulePath string) string {
	if modulePath == "" || !strings.HasPrefix(sym, modulePath+"/") {
		return ""
	}
	rel := strings.TrimPrefix(sym, modulePath+"/") // pkg/traceql.(*Lexer).Next
	prefix, base := "", rel
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		prefix, base = rel[:i], rel[i+1:]
	}
	pkgname := base
	if i := strings.Index(base, "."); i >= 0 {
		pkgname = base[:i]
	}
	if prefix != "" {
		return prefix + "/" + pkgname
	}
	return pkgname
}

// SplitCSV splits a comma-separated flag value into trimmed, non-empty entries.
func SplitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// LoadScope reads scope.json from dir, or nil (whole module in scope) if absent/invalid.
func LoadScope(dir string) *Scope {
	b, err := os.ReadFile(filepath.Join(dir, "scope.json"))
	if err != nil {
		return nil
	}
	var s Scope
	if json.Unmarshal(b, &s) != nil {
		return nil
	}
	return &s
}

func round4(f float64) float64 {
	s := strconv.FormatFloat(f, 'f', 4, 64)
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

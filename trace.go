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

// trace-summary does the MECHANICAL extraction from a dumped OTLP trace (collect-traces writes
// them, often 10MB+): the request-shape attributes (query / endpoint) and the span fan-out (top
// span names by count and by duration). The agent then INTERPRETS this - is the shape pathological
// (a workload pattern), which service/operation is the slow one - instead of hand-rolling jq over
// a large file each time.
type traceSummaryCmd struct {
	File string `arg:"" optional:"" help:"a dumped trace JSON (default: every .go-perf-agent/traces/*.json except *.search.json)"`
	Top  int    `default:"12" help:"how many span names to show"`
}

func (c *traceSummaryCmd) Run() error {
	files := []string{c.File}
	if c.File == "" {
		all, _ := filepath.Glob(filepath.Join(gpaDir, "traces", "*.json"))
		files = nil
		for _, f := range all {
			if !strings.HasSuffix(f, ".search.json") {
				files = append(files, f)
			}
		}
	}
	if len(files) == 0 {
		return fmt.Errorf("no dumped traces found; run collect-traces first (or pass a file)")
	}
	for _, f := range files {
		if err := summarizeTraceFile(f, c.Top); err != nil {
			info("  %s: %v", f, err)
		}
	}
	return nil
}

type otlpTrace struct {
	Trace struct {
		ResourceSpans []struct {
			ScopeSpans []struct {
				Spans []otlpSpan `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	} `json:"trace"`
}

// otlpSpan and otlpValue cover the standard OTLP/JSON shape any Tempo trace returns, for any
// OTel-instrumented Go program - nothing Tempo-specific. All attribute value types are handled.
type otlpSpan struct {
	Name       string `json:"name"`
	Start      string `json:"startTimeUnixNano"`
	End        string `json:"endTimeUnixNano"`
	Attributes []struct {
		Key   string    `json:"key"`
		Value otlpValue `json:"value"`
	} `json:"attributes"`
}

type otlpValue struct {
	StringValue *string  `json:"stringValue"`
	IntValue    *string  `json:"intValue"` // OTLP encodes int64 as a string
	BoolValue   *bool    `json:"boolValue"`
	DoubleValue *float64 `json:"doubleValue"`
}

func (v otlpValue) String() string {
	switch {
	case v.StringValue != nil:
		return *v.StringValue
	case v.IntValue != nil:
		return *v.IntValue
	case v.BoolValue != nil:
		return strconv.FormatBool(*v.BoolValue)
	case v.DoubleValue != nil:
		return strconv.FormatFloat(*v.DoubleValue, 'g', -1, 64)
	}
	return ""
}

// requestShapeKeys are the attributes that define what a request asked for. Generic across
// query-serving systems (search/metrics/SQL/RPC/HTTP), not one engine.
var requestShapeKeys = map[string]bool{
	"query": true, "db.statement": true, "db.query.text": true,
	"http.route": true, "http.target": true, "url.path": true, "rpc.method": true,
}

func summarizeTraceFile(path string, top int) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var t otlpTrace
	if err := json.Unmarshal(b, &t); err != nil {
		return fmt.Errorf("parse OTLP: %w", err)
	}

	type agg struct {
		count int
		sumMs int64
		maxMs int64
	}
	byName := map[string]*agg{}
	shape := map[string]string{} // key -> a representative value
	spanCount := 0
	for _, rs := range t.Trace.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			for _, s := range ss.Spans {
				spanCount++
				a := byName[s.Name]
				if a == nil {
					a = &agg{}
					byName[s.Name] = a
				}
				a.count++
				ms := durMs(s.Start, s.End)
				a.sumMs += ms
				if ms > a.maxMs {
					a.maxMs = ms
				}
				for _, at := range s.Attributes {
					if requestShapeKeys[at.Key] && shape[at.Key] == "" {
						shape[at.Key] = at.Value.String()
					}
				}
			}
		}
	}
	if spanCount == 0 {
		return fmt.Errorf("no spans")
	}

	fmt.Printf("\n## %s  (%d spans)\n", filepath.Base(path), spanCount)
	if len(shape) > 0 {
		fmt.Println("request shape:")
		for _, k := range sortedKeys(shape) {
			fmt.Printf("  %-12s %s\n", k, shape[k])
		}
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	// fan-out: by count (how wide the request spread)
	sort.Slice(names, func(i, j int) bool { return byName[names[i]].count > byName[names[j]].count })
	fmt.Println("fan-out (span name: count, summs, maxms):")
	for i, n := range names {
		if i >= top {
			break
		}
		a := byName[n]
		fmt.Printf("  %-45s n=%-6d sum=%-9d max=%d\n", trunc(n, 45), a.count, a.sumMs, a.maxMs)
	}
	return nil
}

func durMs(start, end string) int64 {
	s, _ := strconv.ParseInt(start, 10, 64)
	e, _ := strconv.ParseInt(end, 10, 64)
	if s == 0 || e == 0 || e < s {
		return 0
	}
	return (e - s) / 1_000_000
}

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

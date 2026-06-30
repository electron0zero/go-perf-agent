package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go-perf-agent/internal/trace"
)

// trace-summary does the MECHANICAL extraction from a dumped OTLP trace (collect-traces writes
// them, often 10MB+): the request-shape attributes (query / endpoint) and the span fan-out (top
// span names by count and by duration). The agent then INTERPRETS it - is the shape pathological
// (a workload pattern), which service/operation is the slow one - instead of hand-rolling jq.
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
		b, err := os.ReadFile(f)
		if err != nil {
			info("  %s: %v", f, err)
			continue
		}
		s, err := trace.Summarize(b)
		if err != nil {
			info("  %s: %v", f, err)
			continue
		}
		printTraceSummary(filepath.Base(f), s, c.Top)
	}
	return nil
}

func printTraceSummary(name string, s *trace.Summary, top int) {
	fmt.Printf("\n## %s  (%d spans)\n", name, s.SpanCount)
	if len(s.Shape) > 0 {
		fmt.Println("request shape:")
		for _, k := range sortedKeys(s.Shape) {
			fmt.Printf("  %-12s %s\n", k, s.Shape[k])
		}
	}
	fmt.Println("fan-out (span name: count, summs, maxms):")
	for i, a := range s.FanOut {
		if i >= top {
			break
		}
		fmt.Printf("  %-45s n=%-6d sum=%-9d max=%d\n", trunc(a.Name, 45), a.Count, a.SumMs, a.MaxMs)
	}
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

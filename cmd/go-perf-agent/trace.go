package main

import (
	"fmt"
	"os"
	"path/filepath"
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
		fmt.Print(trace.Format(filepath.Base(f), s, c.Top))
	}
	return nil
}

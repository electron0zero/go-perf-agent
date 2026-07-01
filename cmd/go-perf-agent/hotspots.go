package main

import (
	"fmt"
	"os"
	"path/filepath"

	"go-perf-agent/internal/hotspot"
	"go-perf-agent/internal/model"
)

type hotspotsCmd struct {
	Pprof string `help:"Explicit pprof file (else glob .go-perf-agent/profiles/)"`
	Top   int    `default:"20" help:"Top N nodes per profile"`
}

func (c *hotspotsCmd) Run() error { return runHotspots(c.Pprof, c.Top) }

// runHotspots ranks profiles into hotspots.json and prints the in-scope candidates. Shared by the
// hotspots command and selftest.
func runHotspots(pprofPath string, topn int) error {
	ensureDirs()
	hots, err := hotspot.Gather(gpaDir, pprofPath, topn, modulePath, info)
	if err != nil {
		return err
	}
	if err := model.WriteJSON(filepath.Join(gpaDir, "hotspots.json"), hots); err != nil {
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
	// the seam where the loop tends to get abandoned: name the remaining stages so ranking is not
	// mistaken for the deliverable. report.md, not a hand-written summary, is the definition of done.
	info("NEXT (do not stop here): form %s/hypotheses.json for these candidates -> `validate`/`bench` each -> `report`. Encode config/architecture/dependency levers AS hypotheses too; the loop is not done until report.md has verdicts.", gpaDir)
	return nil
}

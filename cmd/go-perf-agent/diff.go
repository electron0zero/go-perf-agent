package main

import (
	"fmt"
	"path/filepath"

	"go-perf-agent/internal/diff"
	"go-perf-agent/internal/hotspot"
	"go-perf-agent/internal/model"
)

type targetDiffCmd struct {
	PR          string `help:"GitHub PR url or number (resolved via gh; non-invasive, reads the patch)"`
	Base        string `help:"base ref for a local committed diff, e.g. main (uses <base>...HEAD)"`
	Uncommitted bool   `help:"target working-tree changes vs HEAD (default when no other source given)"`
}

func (c *targetDiffCmd) Run() error {
	ensureDirs()
	var meta diff.Meta
	var err error
	switch {
	case c.PR != "":
		meta, err = diff.FromPR(c.PR, modulePath)
	case c.Base != "":
		meta, err = diff.FromGit(fmt.Sprintf("%s...HEAD", c.Base), "committed", modulePath)
		meta.BaseRef, meta.HeadRef = c.Base, "HEAD"
	default: // uncommitted working tree
		meta, err = diff.FromGit("HEAD", "uncommitted", modulePath)
		meta.BaseRef, meta.HeadRef = "HEAD", "working-tree"
	}
	if err != nil {
		return err
	}
	if len(meta.Funcs) == 0 {
		return fmt.Errorf("no changed Go functions found in the diff (only tests/vendor/generated, or an empty diff)")
	}

	// optional overlay: annotate changed funcs with profile weight if profiles exist
	weights := map[string]float64{}
	if hots, e := hotspot.Gather(gpaDir, "", 200, modulePath, info); e == nil {
		for _, h := range hots {
			weights[h.Symbol] = h.WeightPct
		}
		info("overlay: matched changed funcs against %d profiled symbols", len(hots))
	}

	hotspots := diff.ToHotspots(meta, weights)
	pkgs := diff.Packages(meta)

	// scope to the changed packages, preserving any configured excludes
	excludes := []string{}
	if sc := hotspot.LoadScope(gpaDir); sc != nil {
		excludes = sc.Exclude
	}
	if err := model.WriteJSON(filepath.Join(gpaDir, "scope.json"), hotspot.Scope{Root: ".", Include: pkgs, Exclude: excludes}); err != nil {
		return err
	}
	if err := model.WriteJSON(filepath.Join(gpaDir, "diff.json"), meta); err != nil {
		return err
	}
	if err := model.WriteJSON(filepath.Join(gpaDir, "hotspots.json"), hotspots); err != nil {
		return err
	}

	info("target-diff (%s): %d changed funcs in %d packages -> hotspots.json + scope.json + diff.json",
		meta.Source, len(meta.Funcs), len(pkgs))
	for i, h := range hotspots {
		if i >= 15 {
			break
		}
		w := ""
		if h.WeightPct > 0 {
			w = fmt.Sprintf(" (%.1f%% in profile)", h.WeightPct)
		}
		info("  %d. %s%s", h.Rank, h.Symbol, w)
	}
	return nil
}

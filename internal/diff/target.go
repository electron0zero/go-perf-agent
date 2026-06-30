package diff

import (
	"fmt"
	"path/filepath"

	"go-perf-agent/internal/hotspot"
	"go-perf-agent/internal/model"
)

// TargetOpts selects the diff source: PR (via gh), Base (local committed <base>...HEAD), else the
// uncommitted working tree. Dir is the .go-perf-agent working dir.
type TargetOpts struct {
	PR, Base   string
	Dir        string
	ModulePath string
}

// Target resolves the diff into the candidate set the rest of the loop consumes: it maps changed
// funcs to hotspots (with an optional profile-weight overlay so "changed AND hot" ranks first),
// scopes to the changed packages, and writes hotspots.json + scope.json + diff.json.
func Target(o TargetOpts, logf func(string, ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	var meta Meta
	var err error
	switch {
	case o.PR != "":
		meta, err = FromPR(o.PR, o.ModulePath)
	case o.Base != "":
		meta, err = FromGit(fmt.Sprintf("%s...HEAD", o.Base), "committed", o.ModulePath)
		meta.BaseRef, meta.HeadRef = o.Base, "HEAD"
	default: // uncommitted working tree
		meta, err = FromGit("HEAD", "uncommitted", o.ModulePath)
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
	if hots, e := hotspot.Gather(o.Dir, "", 200, o.ModulePath, logf); e == nil {
		for _, h := range hots {
			weights[h.Symbol] = h.WeightPct
		}
		logf("overlay: matched changed funcs against %d profiled symbols", len(hots))
	}

	hotspots := ToHotspots(meta, weights)
	pkgs := Packages(meta)

	// scope to the changed packages, preserving any configured excludes
	excludes := []string{}
	if sc := hotspot.LoadScope(o.Dir); sc != nil {
		excludes = sc.Exclude
	}
	if err := model.WriteJSON(filepath.Join(o.Dir, "scope.json"), hotspot.Scope{Root: ".", Include: pkgs, Exclude: excludes}); err != nil {
		return err
	}
	if err := model.WriteJSON(filepath.Join(o.Dir, "diff.json"), meta); err != nil {
		return err
	}
	if err := model.WriteJSON(filepath.Join(o.Dir, "hotspots.json"), hotspots); err != nil {
		return err
	}

	logf("target-diff (%s): %d changed funcs in %d packages -> hotspots.json + scope.json + diff.json",
		meta.Source, len(meta.Funcs), len(pkgs))
	for i, h := range hotspots {
		if i >= 15 {
			break
		}
		w := ""
		if h.WeightPct > 0 {
			w = fmt.Sprintf(" (%.1f%% in profile)", h.WeightPct)
		}
		logf("  %d. %s%s", h.Rank, h.Symbol, w)
	}
	return nil
}

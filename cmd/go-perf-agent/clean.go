package main

import (
	"os"
	"path/filepath"

	"go-perf-agent/internal/helper"
)

// clean removes the per-hypothesis git worktrees created under .go-perf-agent/wt/ (full checkouts +
// compiled .test binaries that otherwise pile up across an audit) - --all also clears collected and
// derived artifacts. Review and cherry-pick any proved worktrees before running it.
type cleanCmd struct {
	All bool `help:"also remove all collected + derived artifacts (keeps scope.json)"`
}

func (c *cleanCmd) Run() error {
	wts, _ := filepath.Glob(filepath.Join(gpaDir, "wt", "*"))
	for _, wt := range wts {
		// git worktree remove unregisters and deletes - rm fallback for a stale/unregistered dir.
		if _, _, err := helper.Run("", "git", "worktree", "remove", "--force", wt); err != nil {
			_ = os.RemoveAll(wt)
		}
		info("removed worktree %s", wt)
	}
	_, _, _ = helper.Run("", "git", "worktree", "prune")
	if c.All {
		if err := cleanArtifacts(gpaDir); err != nil {
			return err
		}
		info("removed all artifacts under %s (kept scope.json)", gpaDir)
	}
	if len(wts) == 0 && !c.All {
		info("nothing to clean (no worktrees under %s/wt/)", gpaDir)
	}
	return nil
}

// cleanArtifacts wipes everything under dir except scope.json, which the user sets via `scope` and
// expects to outlive a clean. Wiping by listing (not an enumerated allowlist) so new artifact files
// are cleared without having to update this command.
func cleanArtifacts(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == "scope.json" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

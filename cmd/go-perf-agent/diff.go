package main

import "go-perf-agent/internal/diff"

type targetDiffCmd struct {
	PR          string `help:"GitHub PR url or number (resolved via gh; non-invasive, reads the patch)"`
	Base        string `help:"base ref for a local committed diff, e.g. main (uses <base>...HEAD)"`
	Uncommitted bool   `help:"target working-tree changes vs HEAD (default when no other source given)"`
}

func (c *targetDiffCmd) Run() error {
	ensureDirs()
	return diff.Target(diff.TargetOpts{PR: c.PR, Base: c.Base, Dir: gpaDir, ModulePath: modulePath}, info)
}

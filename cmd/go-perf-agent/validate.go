package main

import "go-perf-agent/internal/gate"

// gateOpts builds the engine options from the CLI config globals.
func gateOpts(id string) gate.Opts {
	return gate.Opts{ID: id, Dir: gpaDir, BenchCount: benchCount, Alpha: alpha}
}

type benchBaselineCmd struct {
	ID string `arg:"" help:"hypothesis id (from .go-perf-agent/hypotheses.json)"`
}

func (c *benchBaselineCmd) Run() error {
	ensureDirs()
	_, err := gate.Baseline(gateOpts(c.ID), info)
	return err
}

type benchVerdictCmd struct {
	ID string `arg:"" help:"hypothesis id"`
}

func (c *benchVerdictCmd) Run() error { return gate.Verdict(gateOpts(c.ID), info) }

// validate: baseline -> apply patch -> verdict, in one shot (non-LLM path).
type validateCmd struct {
	ID    string `arg:"" help:"hypothesis id"`
	Patch string `help:"git patch to apply in the worktree before the verdict"`
}

func (c *validateCmd) Run() error {
	o := gateOpts(c.ID)
	o.Patch = c.Patch
	return gate.Validate(o, info)
}

type validateAllCmd struct{}

func (c *validateAllCmd) Run() error {
	ensureDirs()
	return gate.ValidateAll(gateOpts(""), info)
}

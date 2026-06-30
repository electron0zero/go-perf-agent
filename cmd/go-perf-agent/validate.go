package main

import (
	"fmt"

	"go-perf-agent/internal/gate"
)

// gateOpts builds the engine options from the CLI config globals.
func gateOpts(id string) gate.Opts {
	return gate.Opts{ID: id, Dir: gpaDir, BenchCount: benchCount, Alpha: alpha, MinImprovement: minImprove, RegressionTol: regressTol}
}

// bench groups the benchmark-gate subcommands.
type benchCmd struct {
	Baseline   benchBaselineCmd   `cmd:"" help:"Create worktree + compile the pristine baseline binary"`
	Verdict    benchVerdictCmd    `cmd:"" help:"Tests + interleaved benchmark + benchstat gate"`
	Regression benchRegressionCmd `cmd:"" help:"Compare a benchmark base-vs-head for a regression (no edit)"`
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

// validate validates one hypothesis (baseline -> apply patch -> verdict); --all instead sets up
// baselines for every hypothesis (the per-hypothesis change is then LLM-applied).
type validateCmd struct {
	ID    string `arg:"" optional:"" help:"hypothesis id (omit with --all)"`
	Patch string `help:"git patch to apply in the worktree before the verdict"`
	All   bool   `help:"set up baselines for every hypothesis instead of validating one"`
}

func (c *validateCmd) Run() error {
	if c.All {
		ensureDirs()
		return gate.ValidateAll(gateOpts(""), info)
	}
	if c.ID == "" {
		return fmt.Errorf("validate needs a hypothesis id (or --all)")
	}
	o := gateOpts(c.ID)
	o.Patch = c.Patch
	return gate.Validate(o, info)
}

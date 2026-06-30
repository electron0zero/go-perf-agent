package main

import "go-perf-agent/internal/gate"

// bench-regression: did <head> get slower than <base> for <bench>? Builds the benchmark in a
// worktree at each ref, interleaves them, and reads benchstat with an inverted verdict - a
// significant positive delta on any metric in head is a REGRESSION. No code is edited.
type benchRegressionCmd struct {
	Pkg   string `required:"" help:"package holding the benchmark, e.g. ./pkg/parquet"`
	Bench string `required:"" help:"benchmark name (must exist in BOTH refs)"`
	Base  string `required:"" help:"base git ref, e.g. main"`
	Head  string `default:"HEAD" help:"head git ref"`
	ID    string `default:"regression" help:"label for the run/worktree dirs"`
}

func (c *benchRegressionCmd) Run() error {
	ensureDirs()
	return gate.Regression(gate.RegressionOpts{
		Pkg: c.Pkg, Bench: c.Bench, Base: c.Base, Head: c.Head, ID: c.ID,
		Dir: gpaDir, Alpha: alpha, BenchCount: benchCount,
	}, info)
}

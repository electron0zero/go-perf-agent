package main

import (
	"fmt"
	"os"
	"path/filepath"

	"go-perf-agent/internal/report"
)

type reportCmd struct{}

func (c *reportCmd) Run() error {
	md, err := report.Render(gpaDir)
	if err != nil {
		return err
	}
	out := filepath.Join(gpaDir, "report.md")
	if err := os.WriteFile(out, []byte(md), 0o644); err != nil {
		return err
	}
	info("wrote %s", out)
	fmt.Fprint(os.Stderr, md)
	return nil
}

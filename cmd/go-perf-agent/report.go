package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-perf-agent/internal/helper"
	"go-perf-agent/internal/report"
)

type reportCmd struct {
	Note string `help:"closing note for the report - a line or two, the orchestrator's takeaway"`
}

func (c *reportCmd) Run() error {
	// generate the per-proved patch files the report references before rendering
	if err := report.WritePatches(gpaDir); err != nil {
		return err
	}
	meta := report.Meta{
		Date:        time.Now().Format("2006-01-02T15:04:05Z"),
		Repo:        repoRoot(),
		Commit:      shortHead(),
		ClosingNote: c.Note,
	}
	md, err := report.Render(gpaDir, meta)
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

// repoRoot is the module root the audit runs from (report is invoked there).
func repoRoot() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// shortHead is the commit the worktrees were created from (they are detached off HEAD).
func shortHead() string {
	out, _, err := helper.Run("", "git", "rev-parse", "--short", "HEAD")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(out)
}

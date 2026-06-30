package main

import (
	"fmt"
	"os"
	"path/filepath"

	"go-perf-agent/internal/hotspot"
	"go-perf-agent/internal/model"
)

type scopeCmd struct {
	Root    string `help:"Module root (default: cwd)"`
	Include string `help:"Comma-separated in-scope path prefixes (empty = whole module)"`
	Exclude string `help:"Comma-separated out-of-scope path prefixes"`
	Show    bool   `help:"Print the current scope and exit"`
}

func (c *scopeCmd) Run() error {
	ensureDirs()
	path := filepath.Join(gpaDir, "scope.json")

	if c.Show {
		b, err := os.ReadFile(path)
		if err != nil {
			fmt.Println("no scope set (whole module in scope)")
			return nil
		}
		fmt.Println(string(b))
		return nil
	}

	r := c.Root
	if r == "" {
		r, _ = os.Getwd()
	}
	sc := hotspot.Scope{Root: r, Include: hotspot.SplitCSV(c.Include), Exclude: hotspot.SplitCSV(c.Exclude)}
	if err := model.WriteJSON(path, sc); err != nil {
		return err
	}
	info("wrote %s", path)
	info("  include=%v exclude=%v", sc.Include, sc.Exclude)
	return nil
}

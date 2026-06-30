// Package sys holds the low-level system helpers shared across the engine: running external
// commands (git/go/gcx/benchstat) and a couple of filesystem checks, so callers do not each
// re-implement them.
package sys

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Run executes name in dir (empty = cwd) and returns stdout, stderr, error.
func Run(dir, name string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
	return out.String(), errb.String(), err
}

// Exists reports whether path p exists.
func Exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// MustAbs returns the absolute form of p, or p unchanged if it cannot be resolved.
func MustAbs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

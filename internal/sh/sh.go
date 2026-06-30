// Package sh is the one external-command runner shared by the engine's logic packages, so they
// shell out to git/go/gcx the same way instead of each re-implementing exec.
package sh

import (
	"os/exec"
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

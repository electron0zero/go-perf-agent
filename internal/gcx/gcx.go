// Package gcx wraps the `gcx` CLI: one retrying, diagnosable command runner over sh.Run.
package gcx

import (
	"fmt"
	"strings"
	"time"

	"go-perf-agent/internal/sh"
)

// Run executes `gcx <args>`, retrying transient failures and surfacing gcx's real output on error
// (not a bare "exit status 1"). Permanent failures (size cap / auth / unimplemented) are not retried.
func Run(args ...string) (string, error) {
	var stdout, stderr string
	var err error
	attempts := 0
	for attempt := 1; attempt <= 3; attempt++ {
		attempts = attempt
		stdout, stderr, err = sh.Run("", "gcx", args...)
		if err == nil {
			return stdout, nil
		}
		if permanent(stdout + stderr) {
			break
		}
		if attempt < 3 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
	}
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		detail = strings.TrimSpace(stdout)
	}
	if strings.Contains(detail, "50 MB") || strings.Contains(detail, "exceeds") {
		detail += "\n(narrow the window: smaller --window, or a tighter --from/--to)"
	}
	sub := strings.Join(args[:min(3, len(args))], " ")
	return stdout, fmt.Errorf("gcx %s failed after %d attempt(s): %v: %s", sub, attempts, err, detail)
}

// permanent reports whether gcx output indicates a failure a retry cannot fix.
func permanent(out string) bool {
	for _, s := range []string{"exceeds", "50 MB", "unauthorized", "not yet implemented", "invalid"} {
		if strings.Contains(out, s) {
			return true
		}
	}
	return false
}

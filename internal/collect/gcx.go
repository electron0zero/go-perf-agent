package collect

import (
	"fmt"
	"strings"
	"time"

	"go-perf-agent/internal/helper"
)

// gcxRun executes `gcx <args>`, retrying transient failures and surfacing gcx's real output on error
// (not a bare "exit status 1"). Permanent failures (size cap / auth / unimplemented) are not retried.
func gcxRun(args ...string) (string, error) {
	var stdout, stderr string
	var err error
	attempts := 0
	for attempt := 1; attempt <= 3; attempt++ {
		attempts = attempt
		stdout, stderr, err = helper.Run("", "gcx", args...)
		if err == nil {
			return stdout, nil
		}
		if gcxPermanent(stdout + stderr) {
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

// gcxPermanent reports whether gcx output indicates a failure a retry cannot fix.
func gcxPermanent(out string) bool {
	for _, s := range []string{"exceeds", "50 MB", "unauthorized", "not yet implemented", "invalid"} {
		if strings.Contains(out, s) {
			return true
		}
	}
	return false
}

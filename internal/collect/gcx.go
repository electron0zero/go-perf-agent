package collect

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"go-perf-agent/internal/helper"
)

// gcxRun executes `gcx <args>`, retrying transient failures and surfacing gcx's real output on error
// (not a bare "exit status 1"). Permanent failures (size cap / auth / unimplemented) are not retried -
// each attempt is bounded by gcxTimeout so a stuck connection cannot hang the run.
func gcxRun(args ...string) (string, error) {
	var stdout, stderr string
	var err error
	attempts := 0
	timeout := gcxTimeout()
	for attempt := 1; attempt <= 3; attempt++ {
		attempts = attempt
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		stdout, stderr, err = helper.RunCtx(ctx, "", "gcx", args...)
		cancel()
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

// gcxTimeout bounds each gcx attempt - override with GPA_GCX_TIMEOUT (a Go duration like "90s").
func gcxTimeout() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("GPA_GCX_TIMEOUT")); err == nil && d > 0 {
		return d
	}
	return 120 * time.Second
}

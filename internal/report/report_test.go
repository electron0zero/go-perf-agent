package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// the fixed columns are padded so the table lines up as raw text, not only when rendered.
func TestVerdictTableAligns(t *testing.T) {
	rows := [][5]string{
		{"proved", "h-1", "-80.00%", "0.000", "big win"},
		{"need_more_data", "h-a-much-longer-id", "-", "-", "opt-in needed"},
	}
	lines := strings.Split(strings.TrimRight(verdictTable(rows), "\n"), "\n")
	require.Len(t, lines, 4, "header + separator + 2 rows")
	nthPipe := func(s string, n int) int {
		for i, c := range s {
			if c == '|' {
				if n--; n == 0 {
					return i
				}
			}
		}
		return -1
	}
	// the 5th pipe starts the reason column - equal offset in every line means columns 0-3 align
	want := nthPipe(lines[0], 5)
	require.Positive(t, want)
	for _, l := range lines {
		require.Equal(t, want, nthPipe(l, 5), "reason column starts at the same offset: %q", l)
	}
}

func TestRender(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "runs", "h1")
	require.NoError(t, os.MkdirAll(runDir, 0o755))
	verdict := `{"id":"h1","status":"proved","verdict":{"kept":true,"tests_passed":true,` +
		`"delta":"-19.04%","p_value":"0.001","reason":"significant improvement in sec/op",` +
		`"worktree":".go-perf-agent/wt/h1","benchstat":"Foo-8  100n  90n  -19.04%"}}`
	require.NoError(t, os.WriteFile(filepath.Join(runDir, "verdict.json"), []byte(verdict), 0o644))

	md, err := Render(dir)
	require.NoError(t, err)
	for _, want := range []string{"proved", "h1", "-19.04%", "Proved hypotheses", "Telemetry coverage"} {
		require.Contains(t, md, want)
	}
	// no profiles/traces collected -> coverage flags the production-telemetry gaps
	require.Contains(t, md, "production traces", "coverage flags missing production traces")
}

// no verdicts means VALIDATE was skipped: report must fail loud, not emit an empty report.
func TestRenderNoVerdictsErrors(t *testing.T) {
	_, err := Render(t.TempDir())
	require.Error(t, err, "zero verdicts is a skipped VALIDATE stage")
	require.Contains(t, err.Error(), "VALIDATE stage has not run")
}

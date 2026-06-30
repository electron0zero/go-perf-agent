package report

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

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

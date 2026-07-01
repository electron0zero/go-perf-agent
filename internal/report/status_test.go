package report

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeVerdict(t *testing.T, dir, id, status string) {
	t.Helper()
	rd := filepath.Join(dir, "runs", id)
	require.NoError(t, os.MkdirAll(rd, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(rd, "verdict.json"),
		[]byte(`{"id":"`+id+`","status":"`+status+`"}`), 0o644))
}

func TestLoopStatusComplete(t *testing.T) {
	dir := t.TempDir()

	// nothing done
	ok, reason := LoopStatus(dir).Complete()
	require.False(t, ok)
	require.Contains(t, reason, "not started")

	// two hypotheses, no verdicts -> VALIDATE incomplete
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hypotheses.json"),
		[]byte(`[{"id":"h1"},{"id":"h2"}]`), 0o644))
	ok, reason = LoopStatus(dir).Complete()
	require.False(t, ok)
	require.Contains(t, reason, "VALIDATE incomplete")

	// both verdicts, but no report.md -> REPORT not run
	writeVerdict(t, dir, "h1", "proved")
	writeVerdict(t, dir, "h2", "rejected")
	s := LoopStatus(dir)
	require.Equal(t, 2, s.VerdictTotal())
	require.Equal(t, 1, s.Verdicts["proved"])
	ok, reason = s.Complete()
	require.False(t, ok)
	require.Contains(t, reason, "REPORT not run")

	// report.md present -> complete
	require.NoError(t, os.WriteFile(filepath.Join(dir, "report.md"), []byte("# report"), 0o644))
	ok, _ = LoopStatus(dir).Complete()
	require.True(t, ok, "report + a verdict per hypothesis is complete")
}

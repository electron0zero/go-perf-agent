package gate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVersionPinNote(t *testing.T) {
	dir := t.TempDir()
	require.Empty(t, versionPinNote(dir, "abc123"), "no deployed_version file -> no note")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "deployed_version"), []byte("e703ef6f\n"), 0o644))
	note := versionPinNote(dir, "abc123")
	require.Contains(t, note, "abc123", "names the worktree HEAD")
	require.Contains(t, note, "e703ef6f", "names the deployed ref")
}

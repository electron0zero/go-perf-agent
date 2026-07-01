package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCleanArtifacts(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"scope.json", "hotspots.json", "report.md"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "profiles"), 0o755))

	require.NoError(t, cleanArtifacts(dir))

	require.FileExists(t, filepath.Join(dir, "scope.json"), "user-set scope survives a clean")
	require.NoFileExists(t, filepath.Join(dir, "hotspots.json"))
	require.NoFileExists(t, filepath.Join(dir, "report.md"))
	require.NoDirExists(t, filepath.Join(dir, "profiles"))
}

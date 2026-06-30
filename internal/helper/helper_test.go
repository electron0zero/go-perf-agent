package helper

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRun(t *testing.T) {
	out, _, err := Run("", "echo", "hi")
	require.NoError(t, err)
	require.Equal(t, "hi\n", out)

	_, _, err = Run("", "definitely-not-a-real-binary-xyz")
	require.Error(t, err, "missing binary errors")
}

func TestExists(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	require.False(t, Exists(f), "missing file")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o644))
	require.True(t, Exists(f), "present file")
}

func TestMustAbs(t *testing.T) {
	require.True(t, filepath.IsAbs(MustAbs("x")), "MustAbs returns an absolute path")
}

func TestOrNoop(t *testing.T) {
	OrNoop(nil)("must not panic %d", 1) // nil -> usable no-op
	called := false
	OrNoop(func(string, ...any) { called = true })("x")
	require.True(t, called, "OrNoop(f) returns f")
}

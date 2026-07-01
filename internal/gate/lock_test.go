package gate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAcquireBenchLock(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "bench.lock")

	release, err := acquireBenchLock(dir)
	require.NoError(t, err)
	require.FileExists(t, lock, "lock taken")

	_, err = acquireBenchLock(dir)
	require.Error(t, err, "second acquire fails while the lock is held")

	release()
	require.NoFileExists(t, lock, "release removes the lock")

	release2, err := acquireBenchLock(dir)
	require.NoError(t, err, "re-acquire after release")
	release2()
}

// a lock left by a dead process is stale and must be reclaimed, not block every future run.
func TestAcquireBenchLockReclaimsStale(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "bench.lock")
	require.NoError(t, os.WriteFile(lock, []byte("2147483647\n"), 0o644)) // pid above pid_max - not running

	release, err := acquireBenchLock(dir)
	require.NoError(t, err, "stale lock reclaimed")
	release()
}

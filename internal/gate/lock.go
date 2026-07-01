package gate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// acquireBenchLock takes an exclusive per-module lock so only one bench measurement runs at a time.
// Concurrent benchmarks contend for CPU/cache/memory and defeat the run-by-run interleaving, so a
// second `bench verdict`/`bench regression` on the same module fails fast rather than silently
// producing noise. A lock left by a crashed run (holder pid gone) is reclaimed. Call release with defer.
func acquireBenchLock(dir string) (release func(), err error) {
	lock := filepath.Join(dir, "bench.lock")
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
			_ = f.Close()
			return func() { _ = os.Remove(lock) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if pid, alive := benchLockHolder(lock); alive {
			return nil, fmt.Errorf("another bench measurement (pid %d) is running on this module; benchmarks must run serially - wait for it, or delete %s if it is stale", pid, lock)
		}
		_ = os.Remove(lock) // stale lock from a crashed run: reclaim and retry once
	}
	return nil, fmt.Errorf("could not acquire bench lock %s", lock)
}

// benchLockHolder reports the pid in the lock file and whether it is still alive - signal 0 probes
// existence without touching the process, so a dead holder marks the lock stale.
func benchLockHolder(lock string) (pid int, alive bool) {
	b, err := os.ReadFile(lock)
	if err != nil {
		return 0, false
	}
	pid, err = strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	err = syscall.Kill(pid, 0)
	return pid, err == nil || errors.Is(err, syscall.EPERM)
}

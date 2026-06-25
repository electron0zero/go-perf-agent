package main

import (
	"os"
	"testing"
)

// setModulePath sets the package-global modulePath for tests that exercise resolvePkg, and
// returns a restore func (modulePath is global state shared across the binary).
func setModulePath(p string) func() {
	old := modulePath
	modulePath = p
	return func() { modulePath = old }
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

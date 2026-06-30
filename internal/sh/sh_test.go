package sh

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	out, _, err := Run("", "echo", "hi")
	if err != nil || strings.TrimSpace(out) != "hi" {
		t.Fatalf("echo = (%q, %v), want hi", out, err)
	}

	// dir is respected: pwd run in a temp dir reports that dir
	dir := t.TempDir()
	out, _, err = Run(dir, "pwd")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, filepath.Base(dir)) {
		t.Errorf("pwd in %q = %q, want to contain its basename", dir, out)
	}

	if _, _, err := Run("", "definitely-not-a-real-binary-xyz"); err == nil {
		t.Error("want error for a missing binary")
	}
}

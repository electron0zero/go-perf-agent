package sys

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRun(t *testing.T) {
	if out, _, err := Run("", "echo", "hi"); err != nil || out != "hi\n" {
		t.Errorf("Run echo = (%q, %v), want (\"hi\\n\", nil)", out, err)
	}
	dir := t.TempDir()
	if out, _, err := Run(dir, "pwd"); err != nil || filepath.Base(out) == "" {
		t.Errorf("Run pwd in dir failed: %v", err)
	}
	if _, _, err := Run("", "definitely-not-a-real-binary-xyz"); err == nil {
		t.Error("Run of a missing binary should error")
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	if Exists(f) {
		t.Error("Exists on a missing file = true")
	}
	_ = os.WriteFile(f, []byte("x"), 0o644)
	if !Exists(f) {
		t.Error("Exists on a present file = false")
	}
}

func TestMustAbs(t *testing.T) {
	if got := MustAbs("x"); !filepath.IsAbs(got) {
		t.Errorf("MustAbs(x) = %q, want absolute", got)
	}
}

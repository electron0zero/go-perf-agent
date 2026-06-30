package helper

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRun(t *testing.T) {
	if out, _, err := Run("", "echo", "hi"); err != nil || out != "hi\n" {
		t.Errorf("Run echo = (%q, %v), want (\"hi\\n\", nil)", out, err)
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

func TestOrNoop(t *testing.T) {
	OrNoop(nil)("must not panic %d", 1) // nil -> usable no-op
	called := false
	OrNoop(func(string, ...any) { called = true })("x")
	if !called {
		t.Error("OrNoop(f) did not return f")
	}
}

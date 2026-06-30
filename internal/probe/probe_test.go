package probe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGoMinor(t *testing.T) {
	cases := map[string]int{
		"go version go1.26.4 darwin/arm64": 26,
		"go version go1.23 linux/amd64":    23,
		"go1.21.0":                         21,
		"not a version string":             0,
		"go version devel":                 0,
	}
	for in, want := range cases {
		if got := GoMinor(in); got != want {
			t.Errorf("GoMinor(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestFirstBenchmarkName(t *testing.T) {
	dir := t.TempDir()
	withBench := filepath.Join(dir, "a_test.go")
	if err := os.WriteFile(withBench, []byte("package a\n\nfunc BenchmarkFoo(b *testing.B) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := firstBenchmarkName(withBench); got != "BenchmarkFoo" {
		t.Errorf("firstBenchmarkName = %q, want BenchmarkFoo", got)
	}

	noBench := filepath.Join(dir, "b_test.go")
	if err := os.WriteFile(noBench, []byte("package a\n\nfunc TestFoo(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := firstBenchmarkName(noBench); got != "" {
		t.Errorf("firstBenchmarkName with no bench = %q, want empty", got)
	}
	if got := firstBenchmarkName(filepath.Join(dir, "missing.go")); got != "" {
		t.Errorf("firstBenchmarkName(missing) = %q, want empty", got)
	}
}

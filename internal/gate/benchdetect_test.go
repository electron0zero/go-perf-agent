package gate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDetectBenchmarks(t *testing.T) {
	dir := t.TempDir()
	require.Empty(t, detectBenchmarks(dir), "no test files -> none")

	src := "package p\n\nimport \"testing\"\n\n" +
		"func BenchmarkAlpha(b *testing.B) {}\n" +
		"func BenchmarkBeta(b *testing.B) {}\n" +
		"func TestNotABench(t *testing.T) {}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "p_test.go"), []byte(src), 0o644))

	got := detectBenchmarks(dir)
	require.ElementsMatch(t, []string{"BenchmarkAlpha", "BenchmarkBeta"}, got, "finds benchmarks, not tests")
}

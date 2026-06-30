package evalmod

import (
	"strings"
	"testing"
)

// base has no BenchmarkBuild on purpose: the benchmark is missing in the base ref, so a
// base-vs-head regression check cannot compare and must report INCONCLUSIVE.
func TestBuild(t *testing.T) {
	if Build(5) != strings.Repeat("tok", 5) {
		t.Fatal("wrong output")
	}
}

package evalmod

import (
	"strings"
	"testing"
)

func TestBuild(t *testing.T) {
	if Build(5) != strings.Repeat("tok", 5) {
		t.Fatal("wrong output")
	}
}

// head adds the benchmark, but base lacks it -> the comparison is INCONCLUSIVE.
var R string

func BenchmarkBuild(b *testing.B) {
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		R = Build(500)
	}
}

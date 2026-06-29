package evalmod

import "testing"

func TestSum(t *testing.T) {
	if got := Sum(5); got != 10 {
		t.Fatalf("Sum(5) = %d, want 10", got)
	}
}

var R int

func BenchmarkSum(b *testing.B) {
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		R = Sum(200)
	}
}

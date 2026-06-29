package evalmod

// Sink keeps the inner work from being eliminated by the compiler.
var Sink int

// Sum returns the same result with zero allocations but far more CPU: an honest improvement on
// allocs/op that regresses sec/op, so the gate must REJECT it (the regression guard).
func Sum(n int) int {
	t := 0
	for i := 0; i < n; i++ {
		x := i
		for j := 0; j < 4000; j++ {
			x = (x*1664525 + 1013904223) & 0x7fffffff
		}
		Sink = x
		t += i
	}
	return t
}

package evalmod

// Escaped keeps the slice alive past the call, so it escapes to the heap (1 alloc/op). Cheap CPU.
var Escaped []int

func Sum(n int) int {
	s := make([]int, 0, n)
	for i := 0; i < n; i++ {
		s = append(s, i)
	}
	Escaped = s
	t := 0
	for _, v := range s {
		t += v
	}
	return t
}

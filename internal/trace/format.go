package trace

import (
	"fmt"
	"sort"
	"strings"
)

// Format renders a Summary for display: the request-shape attributes and the top-N span-name
// fan-out. The caller prints the returned string.
func Format(name string, s *Summary, top int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n## %s  (%d spans)\n", name, s.SpanCount)
	if len(s.Shape) > 0 {
		b.WriteString("request shape:\n")
		for _, k := range sortedKeys(s.Shape) {
			fmt.Fprintf(&b, "  %-12s %s\n", k, s.Shape[k])
		}
	}
	b.WriteString("fan-out (span name: count, summs, maxms):\n")
	for i, a := range s.FanOut {
		if i >= top {
			break
		}
		fmt.Fprintf(&b, "  %-45s n=%-6d sum=%-9d max=%d\n", trunc(a.Name, 45), a.Count, a.SumMs, a.MaxMs)
	}
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

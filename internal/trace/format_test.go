package trace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFormat(t *testing.T) {
	s := &Summary{
		SpanCount: 7,
		Shape:     map[string]string{"http.route": "/api/q", "db.statement": "SELECT 1"},
		FanOut: []SpanStat{
			{Name: "scan", Count: 5, SumMs: 500, MaxMs: 200},
			{Name: "merge", Count: 2, SumMs: 50, MaxMs: 40},
			{Name: "encode", Count: 1, SumMs: 10, MaxMs: 10},
		},
	}
	out := Format("trace.json", s, 2)

	require.Contains(t, out, "## trace.json  (7 spans)")
	require.Contains(t, out, "request shape:")
	require.Contains(t, out, "db.statement")
	require.Contains(t, out, "/api/q")
	require.Contains(t, out, "fan-out")
	require.Contains(t, out, "scan")
	require.Contains(t, out, "n=5")
	require.Contains(t, out, "merge")
	require.NotContains(t, out, "encode", "top=2 caps the fan-out list at 2")

	// no shape attributes -> the request-shape section is omitted
	noShape := Format("x", &Summary{SpanCount: 1, FanOut: []SpanStat{{Name: "a", Count: 1}}}, 10)
	require.NotContains(t, noShape, "request shape:")
}

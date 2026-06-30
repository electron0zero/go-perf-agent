package trace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSummarize(t *testing.T) {
	// two spans named "scan" (one slow) + one "root" carrying a request-shape attr.
	j := `{"trace":{"resourceSpans":[{"scopeSpans":[{"spans":[
		{"name":"root","startTimeUnixNano":"1000000000","endTimeUnixNano":"3000000000","attributes":[{"key":"http.route","value":{"stringValue":"/api/q"}}]},
		{"name":"scan","startTimeUnixNano":"1000000000","endTimeUnixNano":"1500000000","attributes":[]},
		{"name":"scan","startTimeUnixNano":"1000000000","endTimeUnixNano":"2000000000","attributes":[]}
	]}]}]}}`
	s, err := Summarize([]byte(j))
	require.NoError(t, err)
	require.Equal(t, 3, s.SpanCount)
	require.Equal(t, "/api/q", s.Shape["http.route"])
	// fan-out sorted by count desc: "scan" (2) first
	require.Equal(t, "scan", s.FanOut[0].Name)
	require.Equal(t, 2, s.FanOut[0].Count)
	require.Equal(t, int64(1000), s.FanOut[0].MaxMs)

	_, err = Summarize([]byte(`{"trace":{"resourceSpans":[]}}`))
	require.Error(t, err, "no spans")
}

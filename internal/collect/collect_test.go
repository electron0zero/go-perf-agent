package collect

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSafeServiceName(t *testing.T) {
	cases := map[string]string{
		"tempo-prod/metrics-generator": "tempo-prod-metrics-generator",
		"a b":                          "a_b",
		"plain":                        "plain",
		"a/b/c":                        "a-b-c",
	}
	for in, want := range cases {
		require.Equal(t, want, SafeServiceName(in), "SafeServiceName(%q)", in)
	}
}

func TestDsOrEnv(t *testing.T) {
	require.Equal(t, "flag", DsOrEnv("flag", "env"), "flag wins")
	require.Equal(t, "env", DsOrEnv("", "env"), "env fallback")
	require.Empty(t, DsOrEnv("", ""))
}

func TestTimeArgs(t *testing.T) {
	require.Equal(t, []string{"--since", "1h"}, TimeArgs("1h", "", ""))
	require.Equal(t, []string{"--from", "now-5m", "--to", "now"}, TimeArgs("1h", "now-5m", "now"), "absolute overrides --since")
	require.Equal(t, []string{"--from", "now-5m"}, TimeArgs("1h", "now-5m", ""))
}

func TestBuildTraceQL(t *testing.T) {
	require.Equal(t, `{ name="x" }`, BuildTraceQL("", "", `{ name="x" }`, "500ms"), "explicit query passes through")
	require.Equal(t,
		`{ resource.service.name = "querier" && resource.service.namespace = "tempo-prod" && duration > 5s }`,
		BuildTraceQL("querier", "tempo-prod", "", "5s"))
	require.Equal(t, "{ duration > 500ms }", BuildTraceQL("", "", "", "500ms"), "duration-only")
}

func TestSummarizeExemplars(t *testing.T) {
	var lines []string
	logf := func(format string, a ...any) { lines = append(lines, fmt.Sprintf(format, a...)) }

	// valid exemplars: header + entries, highest value first
	summarizeExemplars(`{"exemplars":[{"profileId":"p1","spanId":"s1","value":10},{"profileId":"p2","spanId":"s2","value":99}]}`, logf)
	joined := strings.Join(lines, "\n")
	require.Contains(t, joined, "top exemplars by weight")
	require.Less(t, strings.Index(joined, "p2"), strings.Index(joined, "p1"), "p2 (99) sorts before p1 (10)")

	// empty and invalid both log the service-wide fallback
	for _, in := range []string{`{"exemplars":[]}`, `not json`} {
		lines = nil
		summarizeExemplars(in, logf)
		require.Contains(t, strings.Join(lines, "\n"), "no exemplars", "input %q", in)
	}
}

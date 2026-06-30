package gate

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProofLabel(t *testing.T) {
	cases := map[string]string{"ns_op": "sec/op", "B_op": "B/op", "allocs_op": "allocs/op", "bogus": ""}
	for in, want := range cases {
		require.Equal(t, want, proofLabel(in), "proofLabel(%q)", in)
	}
}

func TestParseBenchstat(t *testing.T) {
	// benchstat -format=csv: a per-metric header row "<,>metric,CI..." opens a section, data
	// rows follow; the last two columns are "vs base" and "P".
	csv := `goos: darwin
,base.txt,candidate.txt
,sec/op,CI,sec/op,CI,vs base,P
Foo-8,100n,±1%,90n,±1%,-10.00%,0.002
geomean,,,,,~,
,allocs/op,CI,allocs/op,CI,vs base,P
Foo-8,12,±0%,6,±0%,-50.00%,0.001
`
	d, p := parseBenchstat(csv, "sec/op")
	require.Equal(t, "-10.00%", d)
	require.Equal(t, "0.002", p)

	d, p = parseBenchstat(csv, "allocs/op")
	require.Equal(t, "-50.00%", d)
	require.Equal(t, "0.001", p)

	d, p = parseBenchstat(csv, "B/op")
	require.Empty(t, d, "missing metric")
	require.Empty(t, p)
}

func TestParseBenchstatStripsTrailingN(t *testing.T) {
	csv := `,sec/op,CI,sec/op,CI,vs base,P
Foo-8,100n,±1%,90n,±1%,-10.00%,0.002 (n=6)
`
	_, p := parseBenchstat(csv, "sec/op")
	require.Equal(t, "0.002", p, "trailing n stripped")
}

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

func TestBenchstatRowCount(t *testing.T) {
	single := `,sec/op,CI,sec/op,CI,vs base,P
Foo-8,100n,±1%,90n,±1%,-10.00%,0.002
`
	require.Equal(t, 1, benchstatRowCount(single, "sec/op"))

	// b.Run subtests -> multiple rows (geomean excluded); the gate warns it reads only the first
	subtests := `,sec/op,CI,sec/op,CI,vs base,P
Foo/small-8,100n,±1%,90n,±1%,-10.00%,0.002
Foo/large-8,200n,±1%,180n,±1%,-10.00%,0.003
geomean,,,,,~,
`
	require.Equal(t, 2, benchstatRowCount(subtests, "sec/op"))
	require.Equal(t, 0, benchstatRowCount(subtests, "B/op"), "absent metric")
}

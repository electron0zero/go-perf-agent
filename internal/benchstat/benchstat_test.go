package benchstat

import "testing"

func TestProofLabel(t *testing.T) {
	cases := map[string]string{"ns_op": "sec/op", "B_op": "B/op", "allocs_op": "allocs/op", "bogus": ""}
	for in, want := range cases {
		if got := ProofLabel(in); got != want {
			t.Errorf("ProofLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParse(t *testing.T) {
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
	if d, p := Parse(csv, "sec/op"); d != "-10.00%" || p != "0.002" {
		t.Errorf("sec/op = (%q,%q), want (-10.00%%,0.002)", d, p)
	}
	if d, p := Parse(csv, "allocs/op"); d != "-50.00%" || p != "0.001" {
		t.Errorf("allocs/op = (%q,%q), want (-50.00%%,0.001)", d, p)
	}
	if d, p := Parse(csv, "B/op"); d != "" || p != "" {
		t.Errorf("missing metric = (%q,%q), want empty", d, p)
	}
}

func TestParseStripsTrailingN(t *testing.T) {
	csv := `,sec/op,CI,sec/op,CI,vs base,P
Foo-8,100n,±1%,90n,±1%,-10.00%,0.002 (n=6)
`
	if _, p := Parse(csv, "sec/op"); p != "0.002" {
		t.Errorf("p = %q, want 0.002 (trailing n stripped)", p)
	}
}

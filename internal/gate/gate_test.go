package gate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"go-perf-agent/internal/hotspot"
	"go-perf-agent/internal/model"
)

func TestDecideVerdict(t *testing.T) {
	const minImprove, regressTol = 3.0, 2.0

	// significant improvement above the floor, others not worse -> kept
	win := `,sec/op,CI,sec/op,CI,vs base,P
Foo-8,100n,±1%,90n,±1%,-10.00%,0.002
,B/op,CI,B/op,CI,vs base,P
Foo-8,12,±0%,6,±0%,-50.00%,0.001
`
	kept, delta, p, _ := decideVerdict(win, "sec/op", minImprove, regressTol)
	require.True(t, kept)
	require.Equal(t, "-10.00%", delta)
	require.Equal(t, "0.002", p)

	// a significant improvement below the effect-size floor is noise, not a win -> rejected
	tiny := `,sec/op,CI,sec/op,CI,vs base,P
Foo-8,100n,±1%,99.5n,±1%,-0.50%,0.01
`
	kept, _, _, reason := decideVerdict(tiny, "sec/op", minImprove, regressTol)
	require.False(t, kept, "a 0.5% win is below the 3% floor")
	require.Contains(t, reason, "too small")

	// proof metric improves but another regresses beyond tolerance -> rejected
	tradeoff := `,sec/op,CI,sec/op,CI,vs base,P
Foo-8,100n,±1%,90n,±1%,-10.00%,0.002
,B/op,CI,B/op,CI,vs base,P
Foo-8,10,±0%,12,±0%,+20.00%,0.003
`
	kept, _, _, reason = decideVerdict(tradeoff, "sec/op", minImprove, regressTol)
	require.False(t, kept, "a regression beyond tolerance cancels the win")
	require.Contains(t, reason, "B/op regressed +20.00%", "reason names the regressing metric")

	// a sub-tolerance regression on another metric does NOT cancel a real win -> kept
	withinTol := `,sec/op,CI,sec/op,CI,vs base,P
Foo-8,100n,±1%,90n,±1%,-10.00%,0.002
,B/op,CI,B/op,CI,vs base,P
Foo-8,100,±0%,101,±0%,+1.00%,0.01
`
	kept, _, _, _ = decideVerdict(withinTol, "sec/op", minImprove, regressTol)
	require.True(t, kept, "a 1% B/op regression is within the 2% tolerance")

	// no significant change on the proof metric -> rejected
	noop := `,sec/op,CI,sec/op,CI,vs base,P
Foo-8,100n,±1%,100n,±1%,~,
`
	kept, _, _, _ = decideVerdict(noop, "sec/op", minImprove, regressTol)
	require.False(t, kept, "a ~ (no significant change) is not a win")
}

func TestPctValue(t *testing.T) {
	require.Equal(t, -19.04, pctValue("-19.04%"))
	require.Equal(t, 0.5, pctValue("+0.5%"))
	require.Equal(t, 0.0, pctValue("~"))
}

func TestNeedsDependencyOptIn(t *testing.T) {
	dep := &model.Hypothesis{Dependency: &model.Dependency{Path: "vendor/github.com/x/y", Kind: "vendored-oss"}}
	plain := &model.Hypothesis{}

	require.True(t, needsDependencyOptIn(dep, &hotspot.Scope{Exclude: []string{"vendor"}}), "dep under excluded vendor needs opt-in")
	require.False(t, needsDependencyOptIn(dep, &hotspot.Scope{Include: []string{"vendor/github.com/x/y"}}), "dep with its path scoped in")
	require.False(t, needsDependencyOptIn(plain, &hotspot.Scope{Exclude: []string{"vendor"}}), "plain hypothesis never needs opt-in")
	require.False(t, needsDependencyOptIn(dep, nil), "nil scope = no restriction")
}

func TestTestFilesHash(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	write("a_test.go", "package a\nfunc TestA(t *T){}\n")
	write("b_test.go", "package a\nfunc BenchmarkB(b *B){}\n")

	h1 := testFilesHash(dir)
	require.NotEmpty(t, h1, "hash non-empty with test files present")
	require.Equal(t, h1, testFilesHash(dir), "stable across calls on unchanged files")

	// editing a test file (gaming the ruler) must change the hash
	write("a_test.go", "package a\nfunc TestA(t *T){ _ = 1 }\n")
	require.NotEqual(t, h1, testFilesHash(dir), "hash changes when a _test.go is edited")
}

func TestBenchPkgRel(t *testing.T) {
	cases := map[string]string{
		"./pkg/x/...": "pkg/x", // /... is a go test pattern, not a directory
		"./pkg/x":     "pkg/x",
		"pkg/x":       "pkg/x",
		"./...":       "", // whole-module pattern resolves to module root
	}
	for in, want := range cases {
		require.Equal(t, want, benchPkgRel(in), "benchPkgRel(%q)", in)
	}
}

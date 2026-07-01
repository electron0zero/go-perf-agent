package diff

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const tempoMod = "github.com/grafana/tempo"

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestParseUnifiedRanges(t *testing.T) {
	diff := `diff --git a/x.go b/x.go
--- a/x.go
+++ b/x.go
@@ -10,2 +10,3 @@ func Foo() {
 a
+b
@@ -20,0 +25,2 @@ func Bar() {
+c
+d
diff --git a/y.go b/y.go
--- a/y.go
+++ b/y.go
@@ -1 +1 @@
-old
+new
`
	want := map[string][][2]int{
		"x.go": {{10, 12}, {25, 26}},
		"y.go": {{1, 1}}, // no count => 1 line
	}
	require.Equal(t, want, parseUnifiedRanges(diff))
}

func TestFuncsFromPatchHeaders(t *testing.T) {
	patch := `+++ b/pkg/a/x.go
@@ -10,2 +10,3 @@ func (o *Options) Compile() error {
+x
@@ -50,1 +51,1 @@ func Top() {
+y
+++ b/pkg/a/x_test.go
@@ -1,1 +1,1 @@ func TestThing() {
+z
`
	got := funcsFromPatchHeaders(patch, tempoMod)
	require.Len(t, got, 2, "test file skipped") // x_test.go dropped
	require.Equal(t, "(*Options).Compile", got[0].Func)
	require.Equal(t, "github.com/grafana/tempo/pkg/a.(*Options).Compile", got[0].Symbol)
	require.Equal(t, "Top", got[1].Func)
	require.Equal(t, "github.com/grafana/tempo/pkg/a.Top", got[1].Symbol)
}

func TestFuncsForRanges(t *testing.T) {
	src := "package x\n\nfunc Foo() {\n\treturn\n}\n\nfunc (b *Bar) Baz() int {\n\treturn 1\n}\n"
	path := filepath.Join(t.TempDir(), "x.go")
	writeFile(t, path, src)

	foo := funcsForRanges(path, [][2]int{{4, 4}}, tempoMod)
	require.Len(t, foo, 1)
	require.Equal(t, "Foo", foo[0].Func)
	require.Equal(t, "3-5", foo[0].Lines)

	baz := funcsForRanges(path, [][2]int{{8, 8}}, tempoMod)
	require.Len(t, baz, 1)
	require.Equal(t, "(*Bar).Baz", baz[0].Func)

	require.Empty(t, funcsForRanges(path, [][2]int{{1, 1}}, tempoMod), "package decl matches no func")
}

func TestSymbolFor(t *testing.T) {
	cases := []struct{ pkgDir, recv, name, want string }{
		{"pkg/a", "*Lexer", "Next", "github.com/grafana/tempo/pkg/a.(*Lexer).Next"},
		{"pkg/a", "Lexer", "Next", "github.com/grafana/tempo/pkg/a.Lexer.Next"}, // value receiver keeps no star
		{"pkg/a", "", "Top", "github.com/grafana/tempo/pkg/a.Top"},
		{".", "", "Root", "github.com/grafana/tempo.Root"},
	}
	for _, c := range cases {
		require.Equal(t, c.want, symbolFor(c.pkgDir, c.recv, c.name, tempoMod), "symbolFor(%q,%q,%q)", c.pkgDir, c.recv, c.name)
	}
}

func TestSymbolForNoModule(t *testing.T) {
	require.Empty(t, symbolFor("pkg/a", "*T", "M", ""), "empty modulePath => empty symbol")
}

func TestReceiverFromText(t *testing.T) {
	cases := map[string]string{"o *T": "*T", "o T": "T", "": "", "o T[U]": "T", "*T": "*T"}
	for in, want := range cases {
		require.Equal(t, want, receiverFromText(in), "receiverFromText(%q)", in)
	}
}

func TestSkipGoFile(t *testing.T) {
	skip := []string{"x_test.go", "vendor/a/b.go", "a/vendor/b.go", "x.pb.go", "x.gen.go", "x_gen.go", "README.md"}
	keep := []string{"x.go", "pkg/a/foo.go"}
	for _, f := range skip {
		require.True(t, skipGoFile(f), "skipGoFile(%q) should skip", f)
	}
	for _, f := range keep {
		require.False(t, skipGoFile(f), "skipGoFile(%q) should keep", f)
	}
}

func TestOrFallback(t *testing.T) {
	require.Equal(t, "def", orFallback("", "def"))
	require.Equal(t, "x", orFallback("x", "def"))
}

func TestToHotspots(t *testing.T) {
	meta := Meta{Funcs: []ChangedFunc{
		{Symbol: "m/pkg/a.Foo", Package: "pkg/a", Func: "Foo"},
		{Symbol: "m/pkg/b.Bar", Package: "pkg/b", Func: "Bar"},
		{Symbol: "", Package: "pkg/c", Func: "Baz"}, // no symbol -> fallback pkg.Func
	}}
	weights := map[string]float64{"m/pkg/a.Foo": 5, "m/pkg/b.Bar": 40}

	hots := ToHotspots(meta, weights)
	require.Len(t, hots, 3)
	// ranked by weight desc: Bar(40), Foo(5), Baz(0) - ranks 1..3
	require.Equal(t, "m/pkg/b.Bar", hots[0].Symbol)
	require.Equal(t, 40.0, hots[0].WeightPct)
	require.Equal(t, 1, hots[0].Rank)
	require.Equal(t, "m/pkg/a.Foo", hots[1].Symbol)
	require.Equal(t, 2, hots[1].Rank)
	require.Equal(t, "pkg/c.Baz", hots[2].Symbol, "missing symbol falls back to pkg.Func")
	require.Equal(t, 3, hots[2].Rank)
	// every changed func is a candidate, tagged diff/diff
	for _, h := range hots {
		require.True(t, h.Candidate && h.Editable && h.InScope)
		require.Equal(t, "diff", h.Metric)
		require.Equal(t, "diff", h.Source)
	}
}

func TestPackages(t *testing.T) {
	meta := Meta{Funcs: []ChangedFunc{
		{Package: "pkg/b"}, {Package: "pkg/a"}, {Package: "pkg/b"}, {Package: "pkg/a/sub"},
	}}
	require.Equal(t, []string{"pkg/a", "pkg/a/sub", "pkg/b"}, Packages(meta), "unique + sorted")
	require.Empty(t, Packages(Meta{}))
}

func TestSortFuncs(t *testing.T) {
	fs := []ChangedFunc{
		{Package: "pkg/b", Func: "Z"},
		{Package: "pkg/a", Func: "B"},
		{Package: "pkg/a", Func: "A"},
	}
	sortFuncs(fs)
	// sorted by package, then func name
	require.Equal(t, []ChangedFunc{
		{Package: "pkg/a", Func: "A"},
		{Package: "pkg/a", Func: "B"},
		{Package: "pkg/b", Func: "Z"},
	}, fs)
}

package diff

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const tempoMod = "github.com/grafana/tempo"

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
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
	got := parseUnifiedRanges(diff)
	want := map[string][][2]int{
		"x.go": {{10, 12}, {25, 26}},
		"y.go": {{1, 1}}, // no count => 1 line
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseUnifiedRanges =\n%v\nwant\n%v", got, want)
	}
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
	if len(got) != 2 {
		t.Fatalf("got %d funcs, want 2 (test file skipped): %+v", len(got), got)
	}
	if got[0].Func != "(*Options).Compile" || got[0].Symbol != "github.com/grafana/tempo/pkg/a.(*Options).Compile" {
		t.Errorf("func0 = %+v", got[0])
	}
	if got[1].Func != "Top" || got[1].Symbol != "github.com/grafana/tempo/pkg/a.Top" {
		t.Errorf("func1 = %+v", got[1])
	}
}

func TestFuncsForRanges(t *testing.T) {
	src := "package x\n\nfunc Foo() {\n\treturn\n}\n\nfunc (b *Bar) Baz() int {\n\treturn 1\n}\n"
	path := filepath.Join(t.TempDir(), "x.go")
	writeFile(t, path, src)

	if got := funcsForRanges(path, [][2]int{{4, 4}}, tempoMod); len(got) != 1 || got[0].Func != "Foo" || got[0].Lines != "3-5" {
		t.Errorf("range in Foo gave %+v", got)
	}
	if got := funcsForRanges(path, [][2]int{{8, 8}}, tempoMod); len(got) != 1 || got[0].Func != "(*Bar).Baz" {
		t.Errorf("range in Baz gave %+v", got)
	}
	if got := funcsForRanges(path, [][2]int{{1, 1}}, tempoMod); len(got) != 0 {
		t.Errorf("range in package decl should match no func, got %+v", got)
	}
}

func TestSymbolFor(t *testing.T) {
	cases := []struct{ pkgDir, recv, name, want string }{
		{"pkg/a", "*Lexer", "Next", "github.com/grafana/tempo/pkg/a.(*Lexer).Next"},
		{"pkg/a", "Lexer", "Next", "github.com/grafana/tempo/pkg/a.Lexer.Next"}, // value receiver keeps no star
		{"pkg/a", "", "Top", "github.com/grafana/tempo/pkg/a.Top"},
		{".", "", "Root", "github.com/grafana/tempo.Root"},
	}
	for _, c := range cases {
		if got := symbolFor(c.pkgDir, c.recv, c.name, tempoMod); got != c.want {
			t.Errorf("symbolFor(%q,%q,%q) = %q, want %q", c.pkgDir, c.recv, c.name, got, c.want)
		}
	}
}

func TestSymbolForNoModule(t *testing.T) {
	if got := symbolFor("pkg/a", "*T", "M", ""); got != "" {
		t.Errorf("empty modulePath should give empty symbol, got %q", got)
	}
}

func TestReceiverFromText(t *testing.T) {
	cases := map[string]string{"o *T": "*T", "o T": "T", "": "", "o T[U]": "T", "*T": "*T"}
	for in, want := range cases {
		if got := receiverFromText(in); got != want {
			t.Errorf("receiverFromText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSkipGoFile(t *testing.T) {
	skip := []string{"x_test.go", "vendor/a/b.go", "a/vendor/b.go", "x.pb.go", "x.gen.go", "x_gen.go", "README.md"}
	keep := []string{"x.go", "pkg/a/foo.go"}
	for _, f := range skip {
		if !skipGoFile(f) {
			t.Errorf("skipGoFile(%q) = false, want true", f)
		}
	}
	for _, f := range keep {
		if skipGoFile(f) {
			t.Errorf("skipGoFile(%q) = true, want false", f)
		}
	}
}

func TestOrFallback(t *testing.T) {
	if orFallback("", "def") != "def" || orFallback("x", "def") != "x" {
		t.Error("orFallback wrong")
	}
}

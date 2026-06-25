package main

import (
	"reflect"
	"testing"
)

func TestInScope(t *testing.T) {
	cases := []struct {
		name string
		sc   *Scope
		pkg  string
		want bool
	}{
		{"nil scope = everything", nil, "pkg/a", true},
		{"include match exact", &Scope{Include: []string{"pkg/a"}}, "pkg/a", true},
		{"include match prefix", &Scope{Include: []string{"pkg/a"}}, "pkg/a/b", true},
		{"include miss", &Scope{Include: []string{"pkg/a"}}, "pkg/b", false},
		{"empty include = all", &Scope{}, "anything", true},
		{"exclude wins over include", &Scope{Include: []string{"pkg"}, Exclude: []string{"pkg/a"}}, "pkg/a", false},
		{"not excluded passes", &Scope{Include: []string{"pkg"}, Exclude: []string{"pkg/a"}}, "pkg/b", true},
		{"trailing /... ignored", &Scope{Include: []string{"pkg/a/..."}}, "pkg/a/b", true},
		{"prefix not substring", &Scope{Include: []string{"pkg/a"}}, "pkg/ab", false},
	}
	for _, c := range cases {
		if got := inScope(c.pkg, c.sc); got != c.want {
			t.Errorf("%s: inScope(%q) = %v, want %v", c.name, c.pkg, got, c.want)
		}
	}
}

func TestResolvePkg(t *testing.T) {
	defer setModulePath("github.com/grafana/tempo")()
	cases := map[string]string{
		"github.com/grafana/tempo/pkg/traceql.(*Lexer).Next": "pkg/traceql",
		"github.com/grafana/tempo/tempodb/encoding.Foo":      "tempodb/encoding",
		"unicode.IsSpace":               "", // external
		"runtime.mallocgc":              "", // runtime
		"github.com/other/repo/pkg.Bar": "", // different module
	}
	for sym, want := range cases {
		if got := resolvePkg(sym); got != want {
			t.Errorf("resolvePkg(%q) = %q, want %q", sym, got, want)
		}
	}
}

func TestResolvePkgNoModule(t *testing.T) {
	defer setModulePath("")()
	if got := resolvePkg("github.com/grafana/tempo/pkg/a.F"); got != "" {
		t.Errorf("with empty modulePath, resolvePkg = %q, want empty", got)
	}
}

func TestNeedsDependencyOptIn(t *testing.T) {
	dep := &Hypothesis{Dependency: &Dependency{Path: "vendor/github.com/x/y", Kind: "vendored-oss"}}
	plain := &Hypothesis{}

	if !needsDependencyOptIn(dep, &Scope{Exclude: []string{"vendor"}}) {
		t.Error("dep under excluded vendor should need opt-in")
	}
	if needsDependencyOptIn(dep, &Scope{Include: []string{"vendor/github.com/x/y"}}) {
		t.Error("dep with its path scoped in should NOT need opt-in")
	}
	if needsDependencyOptIn(plain, &Scope{Exclude: []string{"vendor"}}) {
		t.Error("plain hypothesis never needs dependency opt-in")
	}
	if needsDependencyOptIn(dep, nil) {
		t.Error("nil scope = no restriction (matches structural gate), so no opt-in")
	}
}

func TestSplitCSV(t *testing.T) {
	if got := splitCSV("a, b ,,c"); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("splitCSV = %v", got)
	}
	if got := splitCSV(""); got != nil {
		t.Errorf("splitCSV(\"\") = %v, want nil", got)
	}
}

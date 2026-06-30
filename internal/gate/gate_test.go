package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-perf-agent/internal/hotspot"
	"go-perf-agent/internal/model"
)

func TestDecideVerdict(t *testing.T) {
	// significant improvement on the proof metric, others not worse -> kept
	win := `,sec/op,CI,sec/op,CI,vs base,P
Foo-8,100n,±1%,90n,±1%,-10.00%,0.002
,B/op,CI,B/op,CI,vs base,P
Foo-8,12,±0%,6,±0%,-50.00%,0.001
`
	if kept, delta, p, _ := decideVerdict(win, "sec/op"); !kept || delta != "-10.00%" || p != "0.002" {
		t.Errorf("win = kept=%v delta=%q p=%q, want true/-10.00%%/0.002", kept, delta, p)
	}

	// proof metric improves but another metric regresses -> rejected
	tradeoff := `,sec/op,CI,sec/op,CI,vs base,P
Foo-8,100n,±1%,90n,±1%,-10.00%,0.002
,B/op,CI,B/op,CI,vs base,P
Foo-8,10,±0%,12,±0%,+20.00%,0.003
`
	kept, _, _, reason := decideVerdict(tradeoff, "sec/op")
	if kept {
		t.Errorf("tradeoff should reject: a regression on B/op cancels the win")
	}
	if !strings.Contains(reason, "B/op regressed +20.00%") {
		t.Errorf("reason should name the regressing metric, got %q", reason)
	}

	// no significant change on the proof metric -> rejected
	noop := `,sec/op,CI,sec/op,CI,vs base,P
Foo-8,100n,±1%,100n,±1%,~,
`
	if kept, _, _, _ := decideVerdict(noop, "sec/op"); kept {
		t.Error("a ~ (no significant change) should not be kept")
	}
}

func TestNeedsDependencyOptIn(t *testing.T) {
	dep := &model.Hypothesis{Dependency: &model.Dependency{Path: "vendor/github.com/x/y", Kind: "vendored-oss"}}
	plain := &model.Hypothesis{}

	if !needsDependencyOptIn(dep, &hotspot.Scope{Exclude: []string{"vendor"}}) {
		t.Error("dep under excluded vendor should need opt-in")
	}
	if needsDependencyOptIn(dep, &hotspot.Scope{Include: []string{"vendor/github.com/x/y"}}) {
		t.Error("dep with its path scoped in should NOT need opt-in")
	}
	if needsDependencyOptIn(plain, &hotspot.Scope{Exclude: []string{"vendor"}}) {
		t.Error("plain hypothesis never needs dependency opt-in")
	}
	if needsDependencyOptIn(dep, nil) {
		t.Error("nil scope = no restriction (matches structural gate), so no opt-in")
	}
}

func TestTestFilesHash(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a_test.go", "package a\nfunc TestA(t *T){}\n")
	write("b_test.go", "package a\nfunc BenchmarkB(b *B){}\n")

	h1 := testFilesHash(dir)
	if h1 == "" {
		t.Fatal("hash should be non-empty with test files present")
	}
	if h1 != testFilesHash(dir) {
		t.Error("hash must be stable across calls on unchanged files")
	}

	// editing a test file (gaming the ruler) must change the hash
	write("a_test.go", "package a\nfunc TestA(t *T){ _ = 1 }\n")
	if testFilesHash(dir) == h1 {
		t.Error("hash must change when a _test.go file is edited")
	}
}

func TestBenchPkgRel(t *testing.T) {
	cases := map[string]string{
		"./pkg/x/...": "pkg/x", // /... is a go test pattern, not a directory
		"./pkg/x":     "pkg/x",
		"pkg/x":       "pkg/x",
		"./...":       "", // whole-module pattern resolves to module root
	}
	for in, want := range cases {
		if got := benchPkgRel(in); got != want {
			t.Errorf("benchPkgRel(%q) = %q, want %q", in, got, want)
		}
	}
}

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type scopeCmd struct {
	Root    string `help:"Module root (default: cwd)"`
	Include string `help:"Comma-separated in-scope path prefixes (empty = whole module)"`
	Exclude string `help:"Comma-separated out-of-scope path prefixes"`
	Show    bool   `help:"Print the current scope and exit"`
}

func (c *scopeCmd) Run() error {
	ensureDirs()
	path := filepath.Join(gpaDir, "scope.json")

	if c.Show {
		b, err := os.ReadFile(path)
		if err != nil {
			fmt.Println("no scope set (whole module in scope)")
			return nil
		}
		fmt.Println(string(b))
		return nil
	}

	r := c.Root
	if r == "" {
		r, _ = os.Getwd()
	}
	sc := Scope{Root: r, Include: splitCSV(c.Include), Exclude: splitCSV(c.Exclude)}
	if err := writeJSON(path, sc); err != nil {
		return err
	}
	info("wrote %s", path)
	info("  include=%v exclude=%v", sc.Include, sc.Exclude)
	return nil
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// inScope: a package is in scope iff (include empty OR matches an include) AND matches no
// exclude. Exclude wins. Matching is path-prefix, with a trailing "/..." or "/" ignored.
func inScope(pkg string, sc *Scope) bool {
	if sc == nil {
		return true
	}
	norm := func(e string) string { return strings.TrimSuffix(strings.TrimSuffix(e, "/..."), "/") }
	matches := func(list []string) bool {
		for _, e := range list {
			e = norm(e)
			if pkg == e || strings.HasPrefix(pkg, e+"/") {
				return true
			}
		}
		return false
	}
	if matches(sc.Exclude) {
		return false
	}
	if len(sc.Include) == 0 {
		return true
	}
	return matches(sc.Include)
}

// resolvePkg maps a pprof/pyroscope symbol to a repo-relative package dir, or "" if it is not
// ours (stdlib / vendor / runtime), which means not editable.
//
//	github.com/grafana/tempo/pkg/traceql.(*Lexer).Next -> pkg/traceql
//	unicode.IsSpace                                     -> "" (external)
func resolvePkg(sym string) string {
	if modulePath == "" || !strings.HasPrefix(sym, modulePath+"/") {
		return ""
	}
	rel := strings.TrimPrefix(sym, modulePath+"/") // pkg/traceql.(*Lexer).Next
	prefix, base := "", rel
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		prefix, base = rel[:i], rel[i+1:]
	}
	pkgname := base
	if i := strings.Index(base, "."); i >= 0 {
		pkgname = base[:i]
	}
	if prefix != "" {
		return prefix + "/" + pkgname
	}
	return pkgname
}

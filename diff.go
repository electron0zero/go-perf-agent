package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// a function touched by a diff - the unit the loop targets in diff mode.
type changedFunc struct {
	Symbol  string `json:"symbol"`  // module/pkg.(*Recv).Func - best-effort, for profile overlay
	Package string `json:"package"` // repo-relative package dir
	Func    string `json:"func"`    // Name or (*Recv).Name
	File    string `json:"file"`    // repo-relative
	Lines   string `json:"lines"`   // "120-145" (new-file line span of the enclosing func)
}

type diffMeta struct {
	Source  string        `json:"source"` // pr | committed | uncommitted
	PR      string        `json:"pr,omitempty"`
	BaseRef string        `json:"base_ref,omitempty"`
	HeadRef string        `json:"head_ref,omitempty"`
	Funcs   []changedFunc `json:"funcs"`
}

type targetDiffCmd struct {
	PR          string `help:"GitHub PR url or number (resolved via gh; non-invasive, reads the patch)"`
	Base        string `help:"base ref for a local committed diff, e.g. main (uses <base>...HEAD)"`
	Uncommitted bool   `help:"target working-tree changes vs HEAD (default when no other source given)"`
}

func (c *targetDiffCmd) Run() error {
	ensureDirs()
	var meta diffMeta
	var err error
	switch {
	case c.PR != "":
		meta, err = diffFromPR(c.PR)
	case c.Base != "":
		meta, err = diffFromGit(fmt.Sprintf("%s...HEAD", c.Base), "committed")
		meta.BaseRef = c.Base
		meta.HeadRef = "HEAD"
	default: // uncommitted working tree
		meta, err = diffFromGit("HEAD", "uncommitted")
		meta.BaseRef = "HEAD"
		meta.HeadRef = "working-tree"
	}
	if err != nil {
		return err
	}
	if len(meta.Funcs) == 0 {
		return fmt.Errorf("no changed Go functions found in the diff (only tests/vendor/generated, or an empty diff)")
	}

	// optional overlay: annotate changed funcs with profile weight if profiles exist
	weights := map[string]float64{}
	if hots, e := gatherHotspots("", 200); e == nil {
		for _, h := range hots {
			weights[h.Symbol] = h.WeightPct
		}
		info("overlay: matched changed funcs against %d profiled symbols", len(hots))
	}

	// changed funcs become the candidate set (same shape hotspots produces)
	var hotspots []Hotspot
	pkgsSet := map[string]bool{}
	for _, f := range meta.Funcs {
		pkgsSet[f.Package] = true
		hotspots = append(hotspots, Hotspot{
			Symbol: orFallback(f.Symbol, f.Package+"."+f.Func), Package: f.Package,
			WeightPct: weights[f.Symbol], Metric: "diff", Source: "diff",
			Editable: true, InScope: true, Candidate: true,
		})
	}
	// rank: profiled-and-hot first, then diff order
	sort.SliceStable(hotspots, func(i, j int) bool { return hotspots[i].WeightPct > hotspots[j].WeightPct })
	for i := range hotspots {
		hotspots[i].Rank = i + 1
	}

	// scope to the changed packages
	var pkgs []string
	for p := range pkgsSet {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	sc := loadScope()
	excludes := []string{}
	if sc != nil {
		excludes = sc.Exclude
	}
	if err := writeJSON(filepath.Join(gpaDir, "scope.json"), Scope{Root: ".", Include: pkgs, Exclude: excludes}); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(gpaDir, "diff.json"), meta); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(gpaDir, "hotspots.json"), hotspots); err != nil {
		return err
	}

	info("target-diff (%s): %d changed funcs in %d packages -> hotspots.json + scope.json + diff.json",
		meta.Source, len(meta.Funcs), len(pkgs))
	for i, h := range hotspots {
		if i >= 15 {
			break
		}
		w := ""
		if h.WeightPct > 0 {
			w = fmt.Sprintf(" (%.1f%% in profile)", h.WeightPct)
		}
		info("  %d. %s%s", h.Rank, h.Symbol, w)
	}
	return nil
}

// diffFromGit handles local modes: `git diff <spec>` gives changed files + new-file line ranges;
// go/ast over the current files maps those ranges to enclosing functions.
func diffFromGit(spec, source string) (diffMeta, error) {
	out, stderr, err := run("", "git", "diff", spec, "--unified=0", "--", "*.go")
	if err != nil {
		return diffMeta{}, fmt.Errorf("git diff %s failed: %s", spec, stderr)
	}
	byFile := parseUnifiedRanges(out)
	meta := diffMeta{Source: source}
	for file, ranges := range byFile {
		if skipGoFile(file) {
			continue
		}
		meta.Funcs = append(meta.Funcs, funcsForRanges(file, ranges)...)
	}
	sortFuncs(meta.Funcs)
	return meta, nil
}

// diffFromPR reads a PR patch via gh (non-invasive - no checkout). go/ast needs local head
// files which we don't have, so changed funcs come from git's hunk-header function context.
func diffFromPR(pr string) (diffMeta, error) {
	patch, stderr, err := run("", "gh", "pr", "diff", pr, "--patch")
	if err != nil {
		return diffMeta{}, fmt.Errorf("gh pr diff %s failed: %s (is gh authenticated?)", pr, stderr)
	}
	meta := diffMeta{Source: "pr", PR: pr}
	meta.Funcs = funcsFromPatchHeaders(patch)
	sortFuncs(meta.Funcs)
	// best-effort base/head refs
	if v, _, e := run("", "gh", "pr", "view", pr, "--json", "baseRefName,headRefName", "-q", ".baseRefName+\"|\"+.headRefName"); e == nil {
		if parts := strings.SplitN(strings.TrimSpace(v), "|", 2); len(parts) == 2 {
			meta.BaseRef, meta.HeadRef = parts[0], parts[1]
		}
	}
	return meta, nil
}

var hunkRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// parseUnifiedRanges returns, per new file, the changed line ranges in the NEW file.
func parseUnifiedRanges(diff string) map[string][][2]int {
	out := map[string][][2]int{}
	var file string
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+++ b/") {
			file = strings.TrimPrefix(line, "+++ b/")
			continue
		}
		if m := hunkRe.FindStringSubmatch(line); m != nil && file != "" {
			start, _ := strconv.Atoi(m[1])
			count := 1
			if m[2] != "" {
				count, _ = strconv.Atoi(m[2])
			}
			if count == 0 { // pure deletion hunk; attribute to the line above
				count = 1
			}
			out[file] = append(out[file], [2]int{start, start + count - 1})
		}
	}
	return out
}

// funcsForRanges parses file with go/ast and returns the FuncDecls overlapping any range.
func funcsForRanges(file string, ranges [][2]int) []changedFunc {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		return nil
	}
	pkgDir := filepath.Dir(file)
	var out []changedFunc
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		start := fset.Position(fd.Pos()).Line
		end := fset.Position(fd.End()).Line
		for _, r := range ranges {
			if r[0] <= end && r[1] >= start { // overlap
				recv := receiverType(fd)
				full := fd.Name.Name
				if recv != "" {
					full = "(" + recv + ")." + fd.Name.Name
				}
				out = append(out, changedFunc{
					Symbol:  symbolFor(pkgDir, recv, fd.Name.Name),
					Package: pkgDir, Func: full, File: file,
					Lines: fmt.Sprintf("%d-%d", start, end),
				})
				break
			}
		}
	}
	return out
}

var patchFuncRe = regexp.MustCompile(`func(?:\s+\(([^)]*)\))?\s+(\w+)`)

// funcsFromPatchHeaders extracts changed funcs from a unified patch using git's hunk-header
// function context (`@@ ... @@ func (o Options) Compile(...)`).
func funcsFromPatchHeaders(patch string) []changedFunc {
	var out []changedFunc
	seen := map[string]bool{}
	var file string
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "+++ b/") {
			file = strings.TrimPrefix(line, "+++ b/")
			continue
		}
		if !strings.HasPrefix(line, "@@") || file == "" || skipGoFile(file) {
			continue
		}
		ctx := line
		if i := strings.LastIndex(line, "@@"); i >= 0 {
			ctx = line[i+2:]
		}
		m := patchFuncRe.FindStringSubmatch(ctx)
		if m == nil {
			continue
		}
		recv := receiverFromText(m[1])
		full := m[2]
		if recv != "" {
			full = "(" + recv + ")." + m[2]
		}
		pkgDir := filepath.Dir(file)
		key := pkgDir + "." + full
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, changedFunc{
			Symbol: symbolFor(pkgDir, recv, m[2]), Package: pkgDir, Func: full, File: file,
		})
	}
	return out
}

// receiverType renders a FuncDecl receiver as "*Lexer" / "Lexer" (generics index stripped).
func receiverType(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return ""
	}
	return typeName(fd.Recv.List[0].Type)
}

func typeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return "*" + typeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr: // generic receiver: T[U]
		return typeName(t.X)
	case *ast.IndexListExpr:
		return typeName(t.X)
	}
	return ""
}

// receiverFromText extracts the receiver type from hunk-header text like "o Options" / "o *T".
func receiverFromText(recv string) string {
	recv = strings.TrimSpace(recv)
	if recv == "" {
		return ""
	}
	parts := strings.Fields(recv)
	last := parts[len(parts)-1] // "*T" or "T" or "T[U]"
	if i := strings.IndexByte(last, '['); i >= 0 {
		last = last[:i]
	}
	return last
}

// symbolFor builds the pprof-style symbol for overlay matching. recv may carry a leading "*";
// pprof renders pointer-receiver methods as pkg.(*T).M and value-receiver methods as pkg.T.M, so
// preserve that distinction or the profile-weight overlay misses value-receiver methods.
func symbolFor(pkgDir, recv, name string) string {
	if modulePath == "" {
		return ""
	}
	pkgPath := modulePath
	if pkgDir != "." && pkgDir != "" {
		pkgPath = modulePath + "/" + pkgDir
	}
	switch {
	case recv == "":
		return pkgPath + "." + name
	case strings.HasPrefix(recv, "*"):
		return pkgPath + ".(*" + recv[1:] + ")." + name
	default:
		return pkgPath + "." + recv + "." + name
	}
}

func skipGoFile(file string) bool {
	return !strings.HasSuffix(file, ".go") ||
		strings.HasSuffix(file, "_test.go") ||
		strings.HasPrefix(file, "vendor/") || strings.Contains(file, "/vendor/") ||
		strings.HasSuffix(file, ".pb.go") || strings.HasSuffix(file, "_gen.go") ||
		strings.HasSuffix(file, ".gen.go")
}

func sortFuncs(fs []changedFunc) {
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].Package != fs[j].Package {
			return fs[i].Package < fs[j].Package
		}
		return fs[i].Func < fs[j].Func
	})
}

func orFallback(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

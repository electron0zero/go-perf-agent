package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// collect-profiles: pull pyroscope cpu+alloc leaderboards via gcx. These are the primary
// code-level signal (ranked heaviest functions).
type collectProfilesCmd struct {
	Service string `required:"" help:"service_name to profile"`
	Window  string `default:"1h" help:"Time window, e.g. 1h"`
	From    string `help:"Start (RFC3339 / unix / now-1h); overrides --window"`
	To      string `help:"End; overrides --window"`
	Limit   int    `default:"30" help:"Leaderboard size"`
}

func (c *collectProfilesCmd) Run() error {
	ensureDirs()

	sel := fmt.Sprintf(`{service_name="%s"}`, c.Service)
	timeFlags := []string{"--window", c.Window}
	if c.From != "" || c.To != "" {
		f, t := c.From, c.To
		if f == "" {
			f = "now-1h"
		}
		if t == "" {
			t = "now"
		}
		timeFlags = []string{"--from", f, "--to", t}
	}

	for _, kind := range []string{"cpu", "alloc"} {
		pt := cpuPT
		if kind == "alloc" {
			pt = allocPT
		}
		out := filepath.Join(gpaDir, "profiles", fmt.Sprintf("%s.%s.leaderboard.json", safeServiceName(c.Service), kind))
		info("pyroscope %s flamegraph -> %s", kind, out)
		// query (flamegraph), not series --top: series ranks label groups, so a fixed
		// service_name selector yields one row, not a per-function breakdown.
		gcxArgs := []string{"datasources", "pyroscope", "query"}
		if pyroDS != "" {
			gcxArgs = append(gcxArgs, pyroDS)
		}
		gcxArgs = append(gcxArgs, sel, "--profile-type", pt)
		gcxArgs = append(gcxArgs, timeFlags...)
		gcxArgs = append(gcxArgs, "-o", "json")
		stdout, stderr, err := run("", "gcx", gcxArgs...)
		if err != nil {
			fmt.Fprint(os.Stderr, stderr)
			return fmt.Errorf("pyroscope query failed (run 'gcx auth login' if the session expired): %w", err)
		}
		rows, err := flamegraphLeaderboard(stdout, c.Limit)
		if err != nil {
			return fmt.Errorf("%s %s: %w", c.Service, kind, err)
		}
		if err := writeJSON(out, rows); err != nil {
			return err
		}
	}
	fmt.Println(filepath.Join(gpaDir, "profiles"))
	return nil
}

// gcx service_name values can contain a slash (e.g. "namespace/component"); flatten it so
// outputs stay flat in profiles/ instead of spawning a subdir.
func safeServiceName(s string) string {
	return strings.NewReplacer("/", "-", " ", "_").Replace(s)
}

// flamebearer is the flamegraph shape gcx pyroscope query returns: names indexed by node, and
// levels of [offset,total,self,nameIdx,...] quads encoded as strings.
type flamebearer struct {
	Flamegraph struct {
		Names  []string `json:"names"`
		Levels []struct {
			Values []string `json:"values"`
		} `json:"levels"`
	} `json:"flamegraph"`
}

type leaderboardRow struct {
	Function string  `json:"function"`
	Value    float64 `json:"value"`
}

// flamegraphLeaderboard sums self weight per function across the flamegraph - the code-level
// signal hotspots ranks. Returns the top `limit` functions by self weight.
func flamegraphLeaderboard(stdout string, limit int) ([]leaderboardRow, error) {
	var fb flamebearer
	if err := json.Unmarshal([]byte(stdout), &fb); err != nil {
		return nil, fmt.Errorf("parse flamegraph json: %w", err)
	}
	names := fb.Flamegraph.Names
	if len(names) == 0 {
		return nil, fmt.Errorf("flamegraph has no functions (empty profile for this service/window?)")
	}
	self := make([]float64, len(names))
	for _, lvl := range fb.Flamegraph.Levels {
		for i := 0; i+3 < len(lvl.Values); i += 4 {
			s, _ := strconv.ParseFloat(lvl.Values[i+2], 64)
			ni, _ := strconv.Atoi(lvl.Values[i+3])
			if ni >= 0 && ni < len(self) {
				self[ni] += s
			}
		}
	}
	var rows []leaderboardRow
	for i, name := range names {
		if self[i] > 0 && name != "total" && name != "other" {
			rows = append(rows, leaderboardRow{Function: name, Value: self[i]})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Value > rows[j].Value })
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

// collect-traces: gcx has no `tempo query`, so we go through the datasource proxy. Traces
// localize the slow operation (secondary signal).
type collectTracesCmd struct {
	Service   string `help:"resource.service.name to match (for self-observability traces this may carry a deployment prefix, e.g. tempo-querier, not just querier)"`
	Namespace string `help:"Scope the default query to one cluster via resource.namespace; omit to search every cluster the datasource sees"`
	Query     string `help:"Explicit TraceQL (overrides the --service/--namespace default)"`
	Window    string `default:"1h" help:"Time window, e.g. 1h or 30m"`
	DSUID     string `name:"ds-uid" help:"Tempo datasource UID (or GPA_TEMPO_DS_UID)"`
	Limit     int    `default:"20" help:"Max traces"`
}

func (c *collectTracesCmd) Run() error {
	uid := c.DSUID
	if uid == "" {
		uid = tempoDSUID
	}
	if uid == "" {
		return fmt.Errorf("collect-traces: --ds-uid or GPA_TEMPO_DS_UID required (gcx tempo query is not implemented; we use the datasource proxy)")
	}
	ensureDirs()

	q := c.Query
	if q == "" {
		// build the selector from the parts that are set, so a multi-cluster backend can be
		// scoped to one namespace instead of returning every cluster's traces.
		var sel []string
		if c.Service != "" {
			sel = append(sel, fmt.Sprintf(`resource.service.name = "%s"`, c.Service))
		}
		if c.Namespace != "" {
			sel = append(sel, fmt.Sprintf(`resource.namespace = "%s"`, c.Namespace))
		}
		sel = append(sel, "duration > 500ms")
		q = "{ " + strings.Join(sel, " && ") + " }"
	}
	now := time.Now().Unix()
	since := now - int64(windowSeconds(c.Window))
	path := fmt.Sprintf("/api/datasources/proxy/uid/%s/api/search?q=%s&limit=%d&start=%d&end=%d",
		uid, url.QueryEscape(q), c.Limit, since, now)
	name := nameOr(c.Service, "search")
	if c.Namespace != "" { // keep per-namespace outputs distinct so probing doesn't overwrite
		name += "-" + c.Namespace
	}
	out := filepath.Join(gpaDir, "traces", safeServiceName(name)+".json")
	info("tempo search via proxy -> %s", out)
	info("  query: %s", q)

	stdout, stderr, err := run("", "gcx", "api", path, "-o", "json")
	if err != nil {
		fmt.Fprint(os.Stderr, stderr)
		return fmt.Errorf("tempo proxy query failed (check --ds-uid; run 'gcx auth login' if expired)")
	}
	if err := os.WriteFile(out, []byte(stdout), 0o644); err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}

// collect-local: profile a benchmark in this repo with go's own pprof - no Grafana needed.
// This is the fallback when gcx is not set up: point it at the package (and optionally the
// function) the user wants to target, then run hotspots on the resulting profiles.
type collectLocalCmd struct {
	Pkg       string `default:"." help:"single package to benchmark (e.g. ./pkg/parquet) - not ./..."`
	Bench     string `default:"." help:"benchmark name regex to profile (e.g. BenchmarkDecode)"`
	Benchtime string `default:"1s" help:"go test -benchtime"`
	Count     int    `default:"1" help:"go test -count"`
}

func (c *collectLocalCmd) Run() error {
	ensureDirs()
	cpu := mustAbs(filepath.Join(gpaDir, "profiles", "local.cpu.prof"))
	mem := mustAbs(filepath.Join(gpaDir, "profiles", "local.mem.prof"))
	info("profiling %s (bench=%s) -> local.cpu.prof + local.mem.prof", c.Pkg, c.Bench)
	_, stderr, err := run("", "go", "test", "-run=^$", "-bench="+c.Bench, "-benchmem",
		"-benchtime="+c.Benchtime, "-count="+strconv.Itoa(c.Count),
		"-cpuprofile="+cpu, "-memprofile="+mem, c.Pkg)
	if err != nil {
		fmt.Fprint(os.Stderr, stderr)
		return fmt.Errorf("local profiling failed (need a single package with a benchmark matching %q in %s)", c.Bench, c.Pkg)
	}
	info("wrote profiles; now run: go-perf-agent hotspots")
	return nil
}

func windowSeconds(w string) int {
	if strings.HasSuffix(w, "h") {
		if n, err := strconv.Atoi(strings.TrimSuffix(w, "h")); err == nil {
			return n * 3600
		}
	}
	if strings.HasSuffix(w, "m") {
		if n, err := strconv.Atoi(strings.TrimSuffix(w, "m")); err == nil {
			return n * 60
		}
	}
	return 3600
}

func nameOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

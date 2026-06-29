package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/google/pprof/profile"
)

// collect-traces: the FIRST step for production telemetry. It only COLLECTS and DUMPS - it runs
// the TraceQL search and writes the search result plus the full JSON of the slowest traces to
// .go-perf-agent/traces/. The agent (gpa-query-telemetry) then analyzes those dumps to pull the
// key attributes (the query string, http.route, job/block fan-out) - the tool does not analyze.
// Selectors use OTel semantic conventions (resource.service.name / resource.service.namespace).
type collectTracesCmd struct {
	Service     string `help:"OTel service.name (resource.service.name) to match"`
	Namespace   string `help:"OTel service.namespace (resource.service.namespace) to scope to one tenant/cluster"`
	Query       string `help:"Explicit TraceQL (overrides --service/--namespace/--min-duration)"`
	MinDuration string `default:"500ms" help:"Slow threshold for the default query, e.g. 500ms or 5s"`
	Window      string `default:"1h" help:"Relative window, e.g. 1h (gcx --since); ignored when --from/--to are set"`
	From        string `help:"Absolute start (RFC3339 / unix / now-1h) - use to target a past incident window"`
	To          string `help:"Absolute end (RFC3339 / unix / now)"`
	DSUID       string `name:"ds-uid" help:"Tempo datasource UID (or GPA_TEMPO_DS_UID); omit if configured in your gcx context"`
	Limit       int    `default:"20" help:"Max traces in the search result"`
	Dump        int    `default:"3" help:"Fetch and dump this many of the slowest full traces for the agent to analyze (0 = search result only)"`
}

func (c *collectTracesCmd) Run() error {
	ensureDirs()

	q := c.Query
	if q == "" {
		// OTel semantic conventions: service.name / service.namespace (resource scope in TraceQL).
		var sel []string
		if c.Service != "" {
			sel = append(sel, fmt.Sprintf(`resource.service.name = "%s"`, c.Service))
		}
		if c.Namespace != "" {
			sel = append(sel, fmt.Sprintf(`resource.service.namespace = "%s"`, c.Namespace))
		}
		sel = append(sel, "duration > "+c.MinDuration)
		q = "{ " + strings.Join(sel, " && ") + " }"
	}

	uid := dsOrEnv(c.DSUID, tempoDSUID)
	tflags := timeArgs(c.Window, c.From, c.To)
	gcxArgs := append([]string{"datasources", "tempo", "query", q}, tflags...)
	gcxArgs = append(gcxArgs, "--limit", strconv.Itoa(c.Limit), "-o", "json")
	if uid != "" {
		gcxArgs = append(gcxArgs, "-d", uid)
	}

	name := c.Service
	if name == "" {
		name = "search"
	}
	if c.Namespace != "" { // keep per-namespace outputs distinct so probing doesn't overwrite
		name += "-" + c.Namespace
	}
	search := filepath.Join(gpaDir, "traces", safeServiceName(name)+".search.json")
	info("tempo query -> %s", search)
	info("  TraceQL: %s", q)

	stdout, stderr, err := run("", "gcx", gcxArgs...)
	if err != nil {
		fmt.Fprint(os.Stderr, stderr)
		return fmt.Errorf("tempo query failed: %w (run 'gcx auth login' if the session expired; pass --ds-uid or set GPA_TEMPO_DS_UID if no tempo datasource is configured)", err)
	}
	if err := os.WriteFile(search, []byte(stdout), 0o644); err != nil {
		return err
	}
	n := dumpSlowTraces(stdout, c.Dump, uid, tflags)
	info("dumped %d full traces to %s/traces/ (search: %s)", n, gpaDir, filepath.Base(search))
	info("next: the agent analyzes the dumped traces (query / http.route / fan-out), then pivots to profiles")
	return nil
}

// timeArgs returns gcx time flags: an explicit --from/--to window when given (for a past incident
// window), else the relative --since window.
func timeArgs(window, from, to string) []string {
	if from != "" || to != "" {
		var a []string
		if from != "" {
			a = append(a, "--from", from)
		}
		if to != "" {
			a = append(a, "--to", to)
		}
		return a
	}
	return []string{"--since", window}
}

// dumpSlowTraces fetches the full JSON of the `dump` slowest traces and writes each to
// traces/<traceID>.json, so the agent can analyze span attributes locally. Returns how many it wrote.
func dumpSlowTraces(searchJSON string, dump int, uid string, tflags []string) int {
	if dump <= 0 {
		return 0
	}
	var r tempoSearch
	if json.Unmarshal([]byte(searchJSON), &r) != nil || len(r.Traces) == 0 {
		return 0
	}
	sort.Slice(r.Traces, func(i, j int) bool { return r.Traces[i].DurationMs > r.Traces[j].DurationMs })
	wrote := 0
	for i, t := range r.Traces {
		if i >= dump || t.TraceID == "" {
			break
		}
		args := append([]string{"datasources", "tempo", "get", t.TraceID}, tflags...)
		args = append(args, "-o", "json")
		if uid != "" {
			args = append(args, "-d", uid)
		}
		out, stderr, err := run("", "gcx", args...)
		if err != nil {
			fmt.Fprint(os.Stderr, stderr)
			info("  could not fetch trace %s: %v (skipping)", t.TraceID, err)
			continue
		}
		dest := filepath.Join(gpaDir, "traces", t.TraceID+".json")
		if os.WriteFile(dest, []byte(out), 0o644) == nil {
			info("  %dms -> %s", t.DurationMs, filepath.Base(dest))
			wrote++
		}
	}
	return wrote
}

// tempoSearch is the slice of the gcx/Tempo search response we rank by; a small local struct,
// not a tempo import (only these fields matter for picking the slow operation).
type tempoSearch struct {
	Traces []struct {
		TraceID         string `json:"traceID"`
		RootServiceName string `json:"rootServiceName"`
		RootTraceName   string `json:"rootTraceName"`
		DurationMs      int    `json:"durationMs"`
	} `json:"traces"`
}

// collect-exemplars: the trace->profile pivot. Span/profile exemplars link a hot service's
// profiles to concrete profile UUIDs (and trace spans when instrumented with otelpyroscope). The
// agent reads the profileIds and feeds them to collect-profiles --profile-id to scope the profile
// to the slow work. Requires gcx with `pyroscope exemplars` (recent builds).
type collectExemplarsCmd struct {
	Service     string `required:"" help:"service_name to query exemplars for"`
	Kind        string `default:"profile" enum:"profile,span" help:"profile (profileId for drilling) or span (spanId, links to traces)"`
	ProfileType string `help:"Pyroscope profile-type ID (default: cpu)"`
	Window      string `default:"1h" help:"Relative window, e.g. 1h (gcx --since); ignored when --from/--to are set"`
	From        string `help:"Absolute start (RFC3339 / unix / now-1h)"`
	To          string `help:"Absolute end (RFC3339 / unix / now)"`
	DSUID       string `name:"ds-uid" help:"Pyroscope datasource UID (or GPA_PYRO_DS); omit if configured in your gcx context"`
	TopN        int    `default:"100" help:"Max exemplars"`
}

func (c *collectExemplarsCmd) Run() error {
	ensureDirs()
	pt := c.ProfileType
	if pt == "" {
		pt = cpuPT
	}
	sel := fmt.Sprintf(`{service_name="%s"}`, c.Service)
	gcxArgs := append([]string{"datasources", "pyroscope", "exemplars", c.Kind, sel, "--profile-type", pt}, timeArgs(c.Window, c.From, c.To)...)
	gcxArgs = append(gcxArgs, "--top-n", strconv.Itoa(c.TopN), "-o", "json")
	if uid := dsOrEnv(c.DSUID, pyroDS); uid != "" {
		gcxArgs = append(gcxArgs, "-d", uid)
	}

	out := filepath.Join(gpaDir, "profiles", safeServiceName(c.Service)+".exemplars."+c.Kind+".json")
	info("pyroscope %s exemplars -> %s", c.Kind, out)
	stdout, stderr, err := run("", "gcx", gcxArgs...)
	if err != nil {
		fmt.Fprint(os.Stderr, stderr)
		return fmt.Errorf("pyroscope exemplars failed: %w (needs a gcx build with `pyroscope exemplars`; run 'gcx auth login' if expired)", err)
	}
	if err := os.WriteFile(out, []byte(stdout), 0o644); err != nil {
		return err
	}
	summarizeExemplars(stdout)
	info("next: collect-profiles --service %s --profile-id <uuid> ... to scope the profile to these", c.Service)
	return nil
}

type exemplarsResult struct {
	Exemplars []struct {
		ProfileID string `json:"profileId"`
		SpanID    string `json:"spanId"`
		Value     int64  `json:"value"`
	} `json:"exemplars"`
}

func summarizeExemplars(stdout string) {
	var r exemplarsResult
	if json.Unmarshal([]byte(stdout), &r) != nil || len(r.Exemplars) == 0 {
		info("  no exemplars (service may lack span-aware instrumentation); fall back to a service-wide profile")
		return
	}
	sort.Slice(r.Exemplars, func(i, j int) bool { return r.Exemplars[i].Value > r.Exemplars[j].Value })
	info("top exemplars by weight (profileId / spanId):")
	for i, e := range r.Exemplars {
		if i >= 10 {
			break
		}
		fmt.Fprintf(os.Stderr, "  %d  %s  %s\n", e.Value, e.ProfileID, e.SpanID)
	}
}

// collect-profiles: pull pyroscope cpu/alloc/inuse profiles via gcx as real pprof (.pb.gz), which
// hotspots parses with the same google/pprof path as a local profile.
//
// Two ways to scope to the slow work a trace identified:
//
//	--span-id    the value of a slow span's `pyroscope.profile.id` attribute (= the span id).
//	             otel-profiling-go tags profiles with the local root span's id under the `span_id`
//	             label, so this fetches the EXACT profile for that span - the fastest trace->profile
//	             pivot. See https://grafana.com/docs/pyroscope/latest/view-and-analyze-profile-data/traces-to-profiles/
//	--profile-id the profile UUIDs returned by `collect-exemplars`.
type collectProfilesCmd struct {
	Service     string   `required:"" help:"service_name to profile"`
	SpanIDs     []string `name:"span-id" help:"Scope to specific trace spans via the span_id label (the value of a span's pyroscope.profile.id); repeatable - the fastest trace->profile pivot"`
	ProfileIDs  []string `name:"profile-id" help:"Drill into specific profile UUIDs from collect-exemplars (repeatable)"`
	ProfileType string   `help:"Single profile-type ID; default collects cpu+alloc+inuse"`
	Window      string   `default:"1h" help:"Relative window, e.g. 1h (gcx --since); ignored when --from/--to are set"`
	From        string   `help:"Absolute start (RFC3339 / unix / now-1h) - use to target a past incident window"`
	To          string   `help:"Absolute end (RFC3339 / unix / now)"`
	DSUID       string   `name:"ds-uid" help:"Pyroscope datasource UID (or GPA_PYRO_DS); omit if configured in your gcx context"`
}

func (c *collectProfilesCmd) Run() error {
	ensureDirs()

	// cpu (time), alloc (churn), inuse (resident heap - the OOM signal). hotspots ranks each
	// metric separately, so collecting all three keeps memory-residency a first-class signal.
	types := []struct{ kind, pt string }{{"cpu", cpuPT}, {"alloc", allocPT}, {"inuse", inusePT}}
	// a single profile-type (or a --profile-id drill, which is per-type) collects just that one.
	if c.ProfileType != "" {
		types = []struct{ kind, pt string }{{"profile", c.ProfileType}}
	} else if len(c.ProfileIDs) > 0 {
		types = []struct{ kind, pt string }{{"cpu", cpuPT}}
	}

	sel := fmt.Sprintf(`{service_name="%s"}`, c.Service)
	if len(c.SpanIDs) > 0 {
		// span_id = the value of a span's pyroscope.profile.id (otel-profiling-go labels profiles
		// with the local root span's id); fetches the exact profile for those spans.
		sel = fmt.Sprintf(`{service_name="%s", span_id=~"%s"}`, c.Service, strings.Join(c.SpanIDs, "|"))
	}
	uid := dsOrEnv(c.DSUID, pyroDS)
	tflags := timeArgs(c.Window, c.From, c.To)
	collected := 0
	var lastDest string
	for _, k := range types {
		dest := mustAbs(filepath.Join(gpaDir, "profiles", fmt.Sprintf("%s.%s.pb.gz", safeServiceName(c.Service), k.kind)))
		info("pyroscope %s profile -> %s", k.kind, dest)
		gcxArgs := append([]string{"datasources", "pyroscope", "query", sel, "--profile-type", k.pt}, tflags...)
		gcxArgs = append(gcxArgs, "-o", "pprof", "--pprof-path", dest, "--pprof-overwrite")
		if uid != "" {
			gcxArgs = append(gcxArgs, "-d", uid)
		}
		for _, id := range c.ProfileIDs {
			gcxArgs = append(gcxArgs, "--profile-id", id)
		}
		_, stderr, err := run("", "gcx", gcxArgs...)
		if err != nil {
			// non-fatal: a service may lack one profile type (e.g. no inuse); keep the others.
			fmt.Fprint(os.Stderr, stderr)
			info("  %s query failed, skipping: %v", k.kind, err)
			continue
		}
		collected++
		lastDest = dest
	}
	if collected == 0 {
		return fmt.Errorf("pyroscope query returned no profile for %q (run 'gcx auth login' if the session expired; for a 6h+ window the response can exceed gcx's 50MB cap - narrow --window/--from/--to)", c.Service)
	}
	// surface the deployed version so the agent can validate against the matching source ref.
	if v := profileVersion(lastDest); v != "" {
		info("deployed version: %s (validate against this ref, not necessarily HEAD)", v)
	}
	info("next: go-perf-agent hotspots")
	return nil
}

// profileVersion reads the build/version info baked into a collected pprof (Mapping BuildID or a
// version comment) so the agent can warn if the worktree's HEAD differs from the deployed code.
func profileVersion(path string) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	p, err := profile.Parse(f)
	if err != nil {
		return ""
	}
	for _, m := range p.Mapping {
		if m.BuildID != "" {
			return m.BuildID
		}
	}
	for _, c := range p.Comments {
		if strings.Contains(strings.ToLower(c), "git") || strings.Contains(strings.ToLower(c), "version") {
			return c
		}
	}
	return ""
}

// collect-local: profile a benchmark in this repo with go's own pprof - no Grafana needed.
// This is the fallback when gcx is not set up: point it at the package (and optionally the
// function) the user wants to target, then run hotspots on the resulting profiles. Local is the
// only profiles-first path; production starts from traces.
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
	info("next: go-perf-agent hotspots")
	return nil
}

// gcx service_name values can contain a slash (e.g. "namespace/component"); flatten it so
// outputs stay flat in profiles/ instead of spawning a subdir.
func safeServiceName(s string) string {
	return strings.NewReplacer("/", "-", " ", "_").Replace(s)
}

// dsOrEnv prefers the explicit flag, then the env-configured default, then empty (let gcx resolve
// the datasource from its context config).
func dsOrEnv(flag, envDefault string) string {
	if flag != "" {
		return flag
	}
	return envDefault
}

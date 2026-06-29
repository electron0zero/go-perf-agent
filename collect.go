package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// collect-traces: the FIRST step for production telemetry. TraceQL finds the slow operations;
// the agent then pivots to profiles (collect-exemplars + collect-profiles) for that hot service.
// Uses `gcx datasources tempo query` (native search; replaces the old datasource-proxy hack).
type collectTracesCmd struct {
	Service     string `help:"resource.service.name to match (for self-observability traces this may carry a deployment prefix, e.g. tempo-querier, not just querier)"`
	Namespace   string `help:"Scope the default query to one cluster via resource.namespace; omit to search every cluster the datasource sees"`
	Query       string `help:"Explicit TraceQL (overrides the --service/--namespace/--min-duration default)"`
	MinDuration string `default:"500ms" help:"Slow-span threshold for the default query, e.g. 500ms or 1s"`
	Window      string `default:"1h" help:"Time window, e.g. 1h or 30m (passed to gcx --since)"`
	DSUID       string `name:"ds-uid" help:"Tempo datasource UID (or GPA_TEMPO_DS_UID); omit if datasources.tempo is configured in your gcx context"`
	Limit       int    `default:"20" help:"Max traces"`
}

func (c *collectTracesCmd) Run() error {
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
		sel = append(sel, "duration > "+c.MinDuration)
		q = "{ " + strings.Join(sel, " && ") + " }"
	}

	gcxArgs := []string{"datasources", "tempo", "query", q, "--since", c.Window, "--limit", strconv.Itoa(c.Limit), "-o", "json"}
	if uid := dsOrEnv(c.DSUID, tempoDSUID); uid != "" {
		gcxArgs = append(gcxArgs, "-d", uid)
	}

	name := c.Service
	if name == "" {
		name = "search"
	}
	if c.Namespace != "" { // keep per-namespace outputs distinct so probing doesn't overwrite
		name += "-" + c.Namespace
	}
	out := filepath.Join(gpaDir, "traces", safeServiceName(name)+".json")
	info("tempo query -> %s", out)
	info("  TraceQL: %s", q)

	stdout, stderr, err := run("", "gcx", gcxArgs...)
	if err != nil {
		fmt.Fprint(os.Stderr, stderr)
		return fmt.Errorf("tempo query failed: %w (run 'gcx auth login' if the session expired; pass --ds-uid or set GPA_TEMPO_DS_UID if no tempo datasource is configured)", err)
	}
	if err := os.WriteFile(out, []byte(stdout), 0o644); err != nil {
		return err
	}
	summarizeTraces(stdout)
	info("next: pull profiles for the hot service - collect-exemplars then collect-profiles")
	return nil
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

// summarizeTraces prints the slowest operations so the user (and agent) can see which service to
// profile next. Best-effort: a parse failure just skips the summary, the raw JSON is already saved.
func summarizeTraces(stdout string) {
	var r tempoSearch
	if json.Unmarshal([]byte(stdout), &r) != nil || len(r.Traces) == 0 {
		return
	}
	sort.Slice(r.Traces, func(i, j int) bool { return r.Traces[i].DurationMs > r.Traces[j].DurationMs })
	info("slowest operations (rank by duration):")
	for i, t := range r.Traces {
		if i >= 10 {
			break
		}
		fmt.Fprintf(os.Stderr, "  %dms  %s  %s\n", t.DurationMs, t.RootServiceName, t.RootTraceName)
	}
}

// collect-exemplars: the trace->profile pivot. Span/profile exemplars link a hot service's
// profiles to concrete profile UUIDs (and trace spans when instrumented with otelpyroscope). The
// agent reads the profileIds and feeds them to collect-profiles --profile-id to scope the profile
// to the slow work. Requires gcx with `pyroscope exemplars` (recent builds).
type collectExemplarsCmd struct {
	Service     string `required:"" help:"service_name to query exemplars for"`
	Kind        string `default:"profile" enum:"profile,span" help:"profile (profileId for drilling) or span (spanId, links to traces)"`
	ProfileType string `help:"Pyroscope profile-type ID (default: cpu)"`
	Window      string `default:"1h" help:"Time window, e.g. 1h (passed to gcx --since)"`
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
	gcxArgs := []string{"datasources", "pyroscope", "exemplars", c.Kind, sel,
		"--profile-type", pt, "--since", c.Window, "--top-n", strconv.Itoa(c.TopN), "-o", "json"}
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
// hotspots parses with the same google/pprof path as a local profile. Pass --profile-id (from
// collect-exemplars) to scope the profile to the slow spans the traces identified.
type collectProfilesCmd struct {
	Service     string   `required:"" help:"service_name to profile"`
	ProfileIDs  []string `name:"profile-id" help:"Drill into specific profile UUIDs from collect-exemplars (repeatable)"`
	ProfileType string   `help:"Single profile-type ID; default collects cpu+alloc+inuse"`
	Window      string   `default:"1h" help:"Time window, e.g. 1h (passed to gcx --since)"`
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
	uid := dsOrEnv(c.DSUID, pyroDS)
	collected := 0
	for _, k := range types {
		dest := mustAbs(filepath.Join(gpaDir, "profiles", fmt.Sprintf("%s.%s.pb.gz", safeServiceName(c.Service), k.kind)))
		info("pyroscope %s profile -> %s", k.kind, dest)
		gcxArgs := []string{"datasources", "pyroscope", "query", sel,
			"--profile-type", k.pt, "--since", c.Window,
			"-o", "pprof", "--pprof-path", dest, "--pprof-overwrite"}
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
	}
	if collected == 0 {
		return fmt.Errorf("pyroscope query returned no profile for %q (run 'gcx auth login' if the session expired)", c.Service)
	}
	info("next: go-perf-agent hotspots")
	return nil
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

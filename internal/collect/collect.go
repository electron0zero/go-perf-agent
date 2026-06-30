// Package collect pulls production telemetry via the gcx CLI: TraceQL searches + trace dumps,
// pyroscope exemplars (the trace->profile pivot), and cpu/alloc/inuse profiles as pprof. It also
// profiles a local benchmark when gcx is not set up. Logic only; the cmd layer owns flags/config.
package collect

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"go-perf-agent/internal/helper"
	"go-perf-agent/internal/pprof"
)

// SafeServiceName flattens a gcx service_name (which may contain "/") so outputs stay flat in
// profiles/ instead of spawning a subdir.
func SafeServiceName(s string) string {
	return strings.NewReplacer("/", "-", " ", "_").Replace(s)
}

// DsOrEnv prefers the explicit flag, then the env-configured default, then empty (let gcx resolve
// the datasource from its context config).
func DsOrEnv(flag, envDefault string) string {
	if flag != "" {
		return flag
	}
	return envDefault
}

// TimeArgs returns gcx time flags: an explicit --from/--to window when given (for a past incident
// window), else the relative --since window.
func TimeArgs(window, from, to string) []string {
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

// BuildTraceQL returns query verbatim if set, else builds the default slow-trace selector from OTel
// semantic-convention fields (service.name / service.namespace are resource-scoped in TraceQL).
func BuildTraceQL(service, namespace, query, minDuration string) string {
	if query != "" {
		return query
	}
	var sel []string
	if service != "" {
		sel = append(sel, fmt.Sprintf(`resource.service.name = "%s"`, service))
	}
	if namespace != "" {
		sel = append(sel, fmt.Sprintf(`resource.service.namespace = "%s"`, namespace))
	}
	sel = append(sel, "duration > "+minDuration)
	return "{ " + strings.Join(sel, " && ") + " }"
}

// Search is the slice of the gcx/Tempo search response we rank by; a small local struct, not a
// tempo import (only these fields matter for picking the slow operation).
type Search struct {
	Traces []struct {
		TraceID         string `json:"traceID"`
		RootServiceName string `json:"rootServiceName"`
		RootTraceName   string `json:"rootTraceName"`
		DurationMs      int    `json:"durationMs"`
	} `json:"traces"`
}

// Exemplars is the slice of a pyroscope exemplars response we read (profile/span ids by weight).
type Exemplars struct {
	Exemplars []struct {
		ProfileID string `json:"profileId"`
		SpanID    string `json:"spanId"`
		Value     int64  `json:"value"`
	} `json:"exemplars"`
}

// TracesOpts configures a TraceQL search-and-dump. DSUID is already resolved by the caller.
type TracesOpts struct {
	Service, Namespace, Query, MinDuration string
	Window, From, To, DSUID                string
	Limit, Dump                            int
	Dir                                    string
}

// Traces runs the TraceQL search, writes the search result, and dumps the slowest full traces for
// the agent to analyze. It only collects; analysis is a separate step.
func Traces(o TracesOpts, logf func(string, ...any)) error {
	logf = helper.OrNoop(logf)
	q := BuildTraceQL(o.Service, o.Namespace, o.Query, o.MinDuration)
	tflags := TimeArgs(o.Window, o.From, o.To)
	gcxArgs := append([]string{"datasources", "tempo", "query", q}, tflags...)
	gcxArgs = append(gcxArgs, "--limit", strconv.Itoa(o.Limit), "-o", "json")
	if o.DSUID != "" {
		gcxArgs = append(gcxArgs, "-d", o.DSUID)
	}

	name := o.Service
	if name == "" {
		name = "search"
	}
	if o.Namespace != "" { // keep per-namespace outputs distinct so probing doesn't overwrite
		name += "-" + o.Namespace
	}
	search := filepath.Join(o.Dir, "traces", SafeServiceName(name)+".search.json")
	logf("tempo query -> %s", search)
	logf("  TraceQL: %s", q)

	stdout, err := gcxRun(gcxArgs...)
	if err != nil {
		return fmt.Errorf("collect traces: %w\n(run 'gcx auth login' if the session expired; pass --ds-uid or set GPA_TEMPO_DS_UID if no tempo datasource is configured)", err)
	}
	if err := os.WriteFile(search, []byte(stdout), 0o644); err != nil {
		return err
	}
	n := dumpSlowTraces(o.Dir, stdout, o.Dump, o.DSUID, tflags, logf)
	logf("dumped %d full traces to %s/traces/ (search: %s)", n, o.Dir, filepath.Base(search))
	logf("next: the agent analyzes the dumped traces (query / http.route / fan-out), then pivots to profiles")
	return nil
}

// dumpSlowTraces fetches the full JSON of the `dump` slowest traces and writes each to
// traces/<traceID>.json, so the agent can analyze span attributes locally. Returns how many it wrote.
func dumpSlowTraces(dir, searchJSON string, dump int, uid string, tflags []string, logf func(string, ...any)) int {
	if dump <= 0 {
		return 0
	}
	var r Search
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
		out, err := gcxRun(args...)
		if err != nil {
			logf("  could not fetch trace %s: %v (skipping)", t.TraceID, err)
			continue
		}
		dest := filepath.Join(dir, "traces", t.TraceID+".json")
		if os.WriteFile(dest, []byte(out), 0o644) == nil {
			logf("  %dms -> %s", t.DurationMs, filepath.Base(dest))
			wrote++
		}
	}
	return wrote
}

// ExemplarsOpts configures a pyroscope exemplars query. ProfileType defaults to CPUPT when empty.
type ExemplarsOpts struct {
	Service, Kind, ProfileType     string
	Window, From, To, DSUID, CPUPT string
	TopN                           int
	Dir                            string
}

// CollectExemplars queries span/profile exemplars (the trace->profile pivot) and summarizes them.
func CollectExemplars(o ExemplarsOpts, logf func(string, ...any)) error {
	logf = helper.OrNoop(logf)
	pt := o.ProfileType
	if pt == "" {
		pt = o.CPUPT
	}
	sel := fmt.Sprintf(`{service_name="%s"}`, o.Service)
	gcxArgs := append([]string{"datasources", "pyroscope", "exemplars", o.Kind, sel, "--profile-type", pt}, TimeArgs(o.Window, o.From, o.To)...)
	gcxArgs = append(gcxArgs, "--top-n", strconv.Itoa(o.TopN), "-o", "json")
	if o.DSUID != "" {
		gcxArgs = append(gcxArgs, "-d", o.DSUID)
	}

	out := filepath.Join(o.Dir, "profiles", SafeServiceName(o.Service)+".exemplars."+o.Kind+".json")
	logf("pyroscope %s exemplars -> %s", o.Kind, out)
	stdout, err := gcxRun(gcxArgs...)
	if err != nil {
		return fmt.Errorf("collect exemplars: %w\n(needs a gcx build with `pyroscope exemplars`; run 'gcx auth login' if expired)", err)
	}
	if err := os.WriteFile(out, []byte(stdout), 0o644); err != nil {
		return err
	}
	summarizeExemplars(stdout, logf)
	logf("next: collect profiles --service %s --profile-id <uuid> ... to scope the profile to these", o.Service)
	return nil
}

func summarizeExemplars(stdout string, logf func(string, ...any)) {
	var r Exemplars
	if json.Unmarshal([]byte(stdout), &r) != nil || len(r.Exemplars) == 0 {
		logf("  no exemplars (service may lack span-aware instrumentation); fall back to a service-wide profile")
		return
	}
	sort.Slice(r.Exemplars, func(i, j int) bool { return r.Exemplars[i].Value > r.Exemplars[j].Value })
	logf("top exemplars by weight (profileId / spanId):")
	for i, e := range r.Exemplars {
		if i >= 10 {
			break
		}
		logf("  %d  %s  %s", e.Value, e.ProfileID, e.SpanID)
	}
}

// ProfilesOpts configures pyroscope profile collection. ProfileType collects a single type;
// otherwise cpu+alloc+inuse (the CPUPT/AllocPT/InusePT ids) are collected.
type ProfilesOpts struct {
	Service                 string
	SpanIDs, ProfileIDs     []string
	ProfileType             string
	Window, From, To, DSUID string
	CPUPT, AllocPT, InusePT string
	Dir                     string
}

// Profiles pulls pyroscope profiles as real pprof (.pb.gz). It scopes to the slow work via --span-id
// (a span's pyroscope.profile.id) or --profile-id (exemplar UUIDs), else the service-wide profile.
func Profiles(o ProfilesOpts, logf func(string, ...any)) error {
	logf = helper.OrNoop(logf)
	// cpu (time), alloc (churn), inuse (resident heap - the OOM signal). hotspots ranks each
	// metric separately, so collecting all three keeps memory-residency a first-class signal.
	types := []struct{ kind, pt string }{{"cpu", o.CPUPT}, {"alloc", o.AllocPT}, {"inuse", o.InusePT}}
	// a single profile-type (or a --profile-id drill, which is per-type) collects just that one.
	if o.ProfileType != "" {
		types = []struct{ kind, pt string }{{"profile", o.ProfileType}}
	} else if len(o.ProfileIDs) > 0 {
		types = []struct{ kind, pt string }{{"cpu", o.CPUPT}}
	}

	sel := fmt.Sprintf(`{service_name="%s"}`, o.Service)
	if len(o.SpanIDs) > 0 {
		// span_id = the value of a span's pyroscope.profile.id (otel-profiling-go labels profiles
		// with the local root span's id); fetches the exact profile for those spans.
		sel = fmt.Sprintf(`{service_name="%s", span_id=~"%s"}`, o.Service, strings.Join(o.SpanIDs, "|"))
	}
	tflags := TimeArgs(o.Window, o.From, o.To)
	collected := 0
	var lastDest string
	for _, k := range types {
		dest := helper.MustAbs(filepath.Join(o.Dir, "profiles", fmt.Sprintf("%s.%s.pb.gz", SafeServiceName(o.Service), k.kind)))
		logf("pyroscope %s profile -> %s", k.kind, dest)
		gcxArgs := append([]string{"datasources", "pyroscope", "query", sel, "--profile-type", k.pt}, tflags...)
		gcxArgs = append(gcxArgs, "-o", "pprof", "--pprof-path", dest, "--pprof-overwrite")
		if o.DSUID != "" {
			gcxArgs = append(gcxArgs, "-d", o.DSUID)
		}
		for _, id := range o.ProfileIDs {
			gcxArgs = append(gcxArgs, "--profile-id", id)
		}
		if _, err := gcxRun(gcxArgs...); err != nil {
			// non-fatal: a service may lack one profile type (e.g. no inuse); keep the others.
			logf("  %s query failed, skipping: %v", k.kind, err)
			continue
		}
		collected++
		lastDest = dest
	}
	if collected == 0 {
		return fmt.Errorf("pyroscope query returned no profile for %q (run 'gcx auth login' if the session expired; for a 6h+ window the response can exceed gcx's 50MB cap - narrow --window/--from/--to)", o.Service)
	}
	// surface the deployed version so the agent can validate against the matching source ref.
	if v := pprof.Version(lastDest); v != "" {
		logf("deployed version: %s (validate against this ref, not necessarily HEAD)", v)
	}
	logf("next: go-perf-agent hotspots")
	return nil
}

// LocalOpts configures local benchmark profiling (the no-gcx fallback).
type LocalOpts struct {
	Pkg, Bench, Benchtime string
	Count                 int
	Dir                   string
}

// Local profiles a benchmark in this repo with go's own pprof, writing local.cpu.prof + local.mem.prof.
func Local(o LocalOpts, logf func(string, ...any)) error {
	logf = helper.OrNoop(logf)
	cpu := helper.MustAbs(filepath.Join(o.Dir, "profiles", "local.cpu.prof"))
	mem := helper.MustAbs(filepath.Join(o.Dir, "profiles", "local.mem.prof"))
	logf("profiling %s (bench=%s) -> local.cpu.prof + local.mem.prof", o.Pkg, o.Bench)
	_, stderr, err := helper.Run("", "go", "test", "-run=^$", "-bench="+o.Bench, "-benchmem",
		"-benchtime="+o.Benchtime, "-count="+strconv.Itoa(o.Count),
		"-cpuprofile="+cpu, "-memprofile="+mem, o.Pkg)
	if err != nil {
		return fmt.Errorf("local profiling failed (need a single package with a benchmark matching %q in %s): %s", o.Bench, o.Pkg, stderr)
	}
	logf("next: go-perf-agent hotspots")
	return nil
}

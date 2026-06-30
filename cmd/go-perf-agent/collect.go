package main

import "go-perf-agent/internal/collect"

// collect groups telemetry collection: gcx traces/exemplars/profiles, and local benchmark profiling.
type collectCmd struct {
	Traces    collectTracesCmd    `cmd:"" help:"TraceQL: find the slowest operations via gcx tempo [needs auth]"`
	Exemplars collectExemplarsCmd `cmd:"" help:"Pivot from a hot service to its profile UUIDs/spans via gcx [needs auth]"`
	Profiles  collectProfilesCmd  `cmd:"" help:"Pull cpu/alloc/inuse pprof for the hot service via gcx [needs auth]"`
	Local     collectLocalCmd     `cmd:"" help:"Profile a benchmark with go pprof [no telemetry, local data]"`
}

// collect traces: the FIRST step for production telemetry. It only COLLECTS and DUMPS - it runs
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
	return collect.Traces(collect.TracesOpts{
		Service: c.Service, Namespace: c.Namespace, Query: c.Query, MinDuration: c.MinDuration,
		Window: c.Window, From: c.From, To: c.To,
		DSUID: collect.DsOrEnv(c.DSUID, tempoDSUID),
		Limit: c.Limit, Dump: c.Dump, Dir: gpaDir,
	}, info)
}

// collect exemplars: the trace->profile pivot. Span/profile exemplars link a hot service's
// profiles to concrete profile UUIDs (and trace spans when instrumented with otelpyroscope). The
// agent reads the profileIds and feeds them to collect profiles --profile-id to scope the profile
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
	return collect.CollectExemplars(collect.ExemplarsOpts{
		Service: c.Service, Kind: c.Kind, ProfileType: c.ProfileType,
		Window: c.Window, From: c.From, To: c.To,
		DSUID: collect.DsOrEnv(c.DSUID, pyroDS), CPUPT: cpuPT,
		TopN: c.TopN, Dir: gpaDir,
	}, info)
}

// collect profiles: pull pyroscope cpu/alloc/inuse profiles via gcx as real pprof (.pb.gz), which
// hotspots parses with the same google/pprof path as a local profile.
//
// Two ways to scope to the slow work a trace identified:
//
//	--span-id    the value of a slow span's `pyroscope.profile.id` attribute (= the span id).
//	             otel-profiling-go tags profiles with the local root span's id under the `span_id`
//	             label, so this fetches the EXACT profile for that span - the fastest trace->profile
//	             pivot. See https://grafana.com/docs/pyroscope/latest/view-and-analyze-profile-data/traces-to-profiles.md
//	--profile-id the profile UUIDs returned by `collect exemplars`.
type collectProfilesCmd struct {
	Service     string   `required:"" help:"service_name to profile"`
	SpanIDs     []string `name:"span-id" help:"Scope to specific trace spans via the span_id label (the value of a span's pyroscope.profile.id); repeatable - the fastest trace->profile pivot"`
	ProfileIDs  []string `name:"profile-id" help:"Drill into specific profile UUIDs from collect exemplars (repeatable)"`
	ProfileType string   `help:"Single profile-type ID; default collects cpu+alloc+inuse"`
	Window      string   `default:"1h" help:"Relative window, e.g. 1h (gcx --since); ignored when --from/--to are set"`
	From        string   `help:"Absolute start (RFC3339 / unix / now-1h) - use to target a past incident window"`
	To          string   `help:"Absolute end (RFC3339 / unix / now)"`
	DSUID       string   `name:"ds-uid" help:"Pyroscope datasource UID (or GPA_PYRO_DS); omit if configured in your gcx context"`
}

func (c *collectProfilesCmd) Run() error {
	ensureDirs()
	return collect.Profiles(collect.ProfilesOpts{
		Service: c.Service, SpanIDs: c.SpanIDs, ProfileIDs: c.ProfileIDs, ProfileType: c.ProfileType,
		Window: c.Window, From: c.From, To: c.To,
		DSUID: collect.DsOrEnv(c.DSUID, pyroDS),
		CPUPT: cpuPT, AllocPT: allocPT, InusePT: inusePT, Dir: gpaDir,
	}, info)
}

// collect local: profile a benchmark in this repo with go's own pprof - no Grafana needed.
// This is the fallback when gcx is not set up: point it at the package (and optionally the
// function) the user wants to target, then run hotspots on the resulting profiles. Local is the
// only profiles-first path; production starts from traces.
type collectLocalCmd struct {
	Pkg       string `default:"." help:"single package to benchmark (e.g. ./internal/store) - not ./..."`
	Bench     string `default:"." help:"benchmark name regex to profile (e.g. BenchmarkDecode)"`
	Benchtime string `default:"1s" help:"go test -benchtime"`
	Count     int    `default:"1" help:"go test -count"`
}

func (c *collectLocalCmd) Run() error {
	ensureDirs()
	return collect.Local(collect.LocalOpts{
		Pkg: c.Pkg, Bench: c.Bench, Benchtime: c.Benchtime, Count: c.Count, Dir: gpaDir,
	}, info)
}

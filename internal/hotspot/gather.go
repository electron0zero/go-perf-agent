package hotspot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go-perf-agent/internal/pprof"
)

// Gather parses the pprof files under dir/profiles (or a single pprofPath) into a ranked,
// scope-tagged hotspot list. gcx-collected (.pb.gz) and local go-test (.prof) profiles parse the
// same way - the only difference is the local-vs-pyroscope policy in parseProfile. logf takes
// progress lines (pass nil for silent) so this package owns no CLI output.
func Gather(dir, pprofPath string, topn int, modulePath string, logf func(string, ...any)) ([]Hotspot, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	var profs []string
	if pprofPath != "" {
		profs = []string{pprofPath}
	} else {
		for _, g := range []string{"*.pb.gz", "*.prof"} {
			m, _ := filepath.Glob(filepath.Join(dir, "profiles", g))
			profs = append(profs, m...)
		}
	}

	var samples []Sample
	for _, p := range profs {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		logf("parsing pprof %s", p)
		rs, err := parseProfile(p, topn)
		if err != nil {
			logf("  pprof parse failed for %s: %v (skipping)", p, err)
			continue
		}
		samples = append(samples, rs...)
	}
	if len(samples) == 0 {
		return nil, fmt.Errorf("no profiles found. Run `collect profiles` (gcx) or `collect local` (go pprof) first, or pass --pprof FILE")
	}

	sc := LoadScope(dir)
	if sc != nil {
		logf("scope: include=%v exclude=%v", sc.Include, sc.Exclude)
	}
	return Rank(samples, sc, modulePath), nil
}

// parseProfile maps a pprof file to self-weight samples, applying our local-vs-pyroscope policy on
// top of the pure pprof parse: local profiles are written as local.*.prof (their inuse is
// meaningless, so skip it), gcx-collected ones come from pyroscope.
func parseProfile(path string, topn int) ([]Sample, error) {
	local := strings.HasPrefix(filepath.Base(path), "local.")
	ws, err := pprof.ParseFlat(path, topn, local)
	if err != nil {
		return nil, err
	}
	source := "pyroscope"
	if local {
		source = "local-pprof"
	}
	out := make([]Sample, len(ws))
	for i, w := range ws {
		out[i] = Sample{Value: w.Value, Symbol: w.Func, Metric: w.Metric, Source: source}
	}
	return out, nil
}

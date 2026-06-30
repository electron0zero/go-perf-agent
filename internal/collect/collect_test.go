package collect

import (
	"reflect"
	"testing"
)

func TestSafeServiceName(t *testing.T) {
	cases := map[string]string{
		"tempo-prod/metrics-generator": "tempo-prod-metrics-generator",
		"a b":                          "a_b",
		"plain":                        "plain",
		"a/b/c":                        "a-b-c",
	}
	for in, want := range cases {
		if got := SafeServiceName(in); got != want {
			t.Errorf("SafeServiceName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDsOrEnv(t *testing.T) {
	if got := DsOrEnv("flag", "env"); got != "flag" {
		t.Errorf("flag should win, got %q", got)
	}
	if got := DsOrEnv("", "env"); got != "env" {
		t.Errorf("env fallback failed, got %q", got)
	}
	if got := DsOrEnv("", ""); got != "" {
		t.Errorf("empty should stay empty, got %q", got)
	}
}

func TestTimeArgs(t *testing.T) {
	if got := TimeArgs("1h", "", ""); !reflect.DeepEqual(got, []string{"--since", "1h"}) {
		t.Errorf("relative window = %v, want --since 1h", got)
	}
	if got := TimeArgs("1h", "now-5m", "now"); !reflect.DeepEqual(got, []string{"--from", "now-5m", "--to", "now"}) {
		t.Errorf("absolute window = %v, want --from/--to (overrides --since)", got)
	}
	if got := TimeArgs("1h", "now-5m", ""); !reflect.DeepEqual(got, []string{"--from", "now-5m"}) {
		t.Errorf("from only = %v", got)
	}
}

func TestBuildTraceQL(t *testing.T) {
	if got := BuildTraceQL("", "", `{ name="x" }`, "500ms"); got != `{ name="x" }` {
		t.Errorf("explicit query should pass through, got %q", got)
	}
	got := BuildTraceQL("querier", "tempo-prod", "", "5s")
	want := `{ resource.service.name = "querier" && resource.service.namespace = "tempo-prod" && duration > 5s }`
	if got != want {
		t.Errorf("BuildTraceQL =\n %q\nwant\n %q", got, want)
	}
	if got := BuildTraceQL("", "", "", "500ms"); got != "{ duration > 500ms }" {
		t.Errorf("duration-only = %q", got)
	}
}

package main

import "testing"

func TestSafeServiceName(t *testing.T) {
	cases := map[string]string{
		"tempo-prod/metrics-generator": "tempo-prod-metrics-generator",
		"a b":                          "a_b",
		"plain":                        "plain",
		"a/b/c":                        "a-b-c",
	}
	for in, want := range cases {
		if got := safeServiceName(in); got != want {
			t.Errorf("safeServiceName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDsOrEnv(t *testing.T) {
	if got := dsOrEnv("flag", "env"); got != "flag" {
		t.Errorf("flag should win, got %q", got)
	}
	if got := dsOrEnv("", "env"); got != "env" {
		t.Errorf("env fallback failed, got %q", got)
	}
	if got := dsOrEnv("", ""); got != "" {
		t.Errorf("empty should stay empty, got %q", got)
	}
}

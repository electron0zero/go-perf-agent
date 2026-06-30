package main

import (
	"encoding/json"
	"testing"
)

func TestGrade(t *testing.T) {
	cases := []struct {
		name     string
		expected string
		got      []string
		want     string
	}{
		{"all match", "proved", []string{"proved", "proved", "proved"}, "PASS"},
		{"none match", "proved", []string{"rejected", "rejected"}, "FAIL"},
		{"some match + varies", "proved", []string{"proved", "rejected", "proved"}, "FLAKY"},
		{"all wrong but identical", "proved", []string{"rejected", "rejected"}, "FAIL"},
		{"single match", "clean", []string{"clean"}, "PASS"},
	}
	for _, c := range cases {
		if got := grade(c.expected, c.got); got != c.want {
			t.Errorf("%s: grade(%q, %v) = %q, want %q", c.name, c.expected, c.got, got, c.want)
		}
	}
}

func TestHypothesisID(t *testing.T) {
	if got := hypothesisID(json.RawMessage(`{"id":"h7","pattern":"x"}`)); got != "h7" {
		t.Errorf("hypothesisID = %q, want h7", got)
	}
	if got := hypothesisID(json.RawMessage(`{"pattern":"x"}`)); got != "h" {
		t.Errorf("hypothesisID with no id = %q, want fallback h", got)
	}
}

package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
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
		require.Equal(t, c.want, grade(c.expected, c.got), c.name)
	}
}

func TestHypothesisID(t *testing.T) {
	require.Equal(t, "h7", hypothesisID(json.RawMessage(`{"id":"h7","pattern":"x"}`)))
	require.Equal(t, "h", hypothesisID(json.RawMessage(`{"pattern":"x"}`)), "no id falls back to h")
}

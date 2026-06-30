package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGoMinor(t *testing.T) {
	cases := map[string]int{
		"go version go1.26.4 darwin/arm64": 26,
		"go version go1.23 linux/amd64":    23,
		"go1.21.0":                         21,
		"not a version string":             0,
		"go version devel":                 0,
	}
	for in, want := range cases {
		require.Equal(t, want, goMinor(in), "goMinor(%q)", in)
	}
}

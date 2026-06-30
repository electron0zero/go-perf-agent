package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvInt(t *testing.T) {
	t.Setenv("GPA_TEST_INT", "7")
	require.Equal(t, 7, envInt("GPA_TEST_INT", 3), "set")
	t.Setenv("GPA_TEST_INT", "notnum")
	require.Equal(t, 3, envInt("GPA_TEST_INT", 3), "invalid falls back to default")
	require.Equal(t, 5, envInt("GPA_TEST_UNSET_XYZ", 5), "unset falls back to default")
}

func TestEnv(t *testing.T) {
	t.Setenv("GPA_TEST_STR", "v")
	require.Equal(t, "v", env("GPA_TEST_STR", "def"), "set")
	require.Equal(t, "def", env("GPA_TEST_UNSET_XYZ", "def"), "unset falls back to default")
}

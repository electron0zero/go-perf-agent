package main

import "testing"

func TestEnvInt(t *testing.T) {
	t.Setenv("GPA_TEST_INT", "7")
	if got := envInt("GPA_TEST_INT", 3); got != 7 {
		t.Errorf("envInt set = %d, want 7", got)
	}
	t.Setenv("GPA_TEST_INT", "notnum")
	if got := envInt("GPA_TEST_INT", 3); got != 3 {
		t.Errorf("envInt invalid = %d, want default 3", got)
	}
	if got := envInt("GPA_TEST_UNSET_XYZ", 5); got != 5 {
		t.Errorf("envInt unset = %d, want default 5", got)
	}
}

func TestEnv(t *testing.T) {
	t.Setenv("GPA_TEST_STR", "v")
	if got := env("GPA_TEST_STR", "def"); got != "v" {
		t.Errorf("env set = %q, want v", got)
	}
	if got := env("GPA_TEST_UNSET_XYZ", "def"); got != "def" {
		t.Errorf("env unset = %q, want def", got)
	}
}

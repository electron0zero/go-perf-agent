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

func TestWindowSeconds(t *testing.T) {
	cases := map[string]int{"1h": 3600, "6h": 21600, "30m": 1800, "90m": 5400, "bad": 3600, "": 3600}
	for in, want := range cases {
		if got := windowSeconds(in); got != want {
			t.Errorf("windowSeconds(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestNameOr(t *testing.T) {
	if nameOr("", "def") != "def" || nameOr("svc", "def") != "svc" {
		t.Error("nameOr wrong")
	}
}

func TestFlamegraphLeaderboard(t *testing.T) {
	// flamebearer: names[] indexed by node; levels[] are [offset,total,self,nameIdx,...] quads.
	// foo self = 40 (lvl1) + 10 (lvl2) = 50; bar = 30; baz = 20; total/other excluded.
	in := `{"flamegraph":{
		"names":["total","other","foo","bar","baz"],
		"levels":[
			{"values":["0","100","0","0"]},
			{"values":["0","60","40","2","0","40","30","3"]},
			{"values":["0","20","10","2","0","20","20","4"]}
		]}}`

	rows, err := flamegraphLeaderboard(in, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []leaderboardRow{{"foo", 50}, {"bar", 30}, {"baz", 20}}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(rows), len(want), rows)
	}
	for i, w := range want {
		if rows[i] != w {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], w)
		}
	}

	// limit truncates after sorting by self weight
	rows, _ = flamegraphLeaderboard(in, 2)
	if len(rows) != 2 || rows[0].Function != "foo" || rows[1].Function != "bar" {
		t.Errorf("limit=2 gave %+v", rows)
	}
}

func TestFlamegraphLeaderboardEmpty(t *testing.T) {
	if _, err := flamegraphLeaderboard(`{"flamegraph":{"names":[],"levels":[]}}`, 0); err == nil {
		t.Error("want error for empty flamegraph, got nil")
	}
	if _, err := flamegraphLeaderboard(`not json`, 0); err == nil {
		t.Error("want error for invalid json, got nil")
	}
}

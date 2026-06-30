package trace

import "testing"

func TestSummarize(t *testing.T) {
	// two spans named "scan" (one slow) + one "root" carrying a request-shape attr.
	j := `{"trace":{"resourceSpans":[{"scopeSpans":[{"spans":[
		{"name":"root","startTimeUnixNano":"1000000000","endTimeUnixNano":"3000000000","attributes":[{"key":"http.route","value":{"stringValue":"/api/q"}}]},
		{"name":"scan","startTimeUnixNano":"1000000000","endTimeUnixNano":"1500000000","attributes":[]},
		{"name":"scan","startTimeUnixNano":"1000000000","endTimeUnixNano":"2000000000","attributes":[]}
	]}]}]}}`
	s, err := Summarize([]byte(j))
	if err != nil {
		t.Fatal(err)
	}
	if s.SpanCount != 3 {
		t.Fatalf("spanCount = %d, want 3", s.SpanCount)
	}
	if s.Shape["http.route"] != "/api/q" {
		t.Errorf("shape http.route = %q, want /api/q", s.Shape["http.route"])
	}
	// fan-out sorted by count desc: "scan" (2) first
	if s.FanOut[0].Name != "scan" || s.FanOut[0].Count != 2 || s.FanOut[0].MaxMs != 1000 {
		t.Errorf("fanout[0] = %+v, want scan/2/maxMs=1000", s.FanOut[0])
	}
	if _, err := Summarize([]byte(`{"trace":{"resourceSpans":[]}}`)); err == nil {
		t.Error("want error for no spans")
	}
}

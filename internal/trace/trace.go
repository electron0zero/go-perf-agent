// Package trace summarizes a dumped OTLP/JSON trace into its request shape (query / endpoint
// attributes) and span fan-out. It parses standard OTLP only - works for any OTel-instrumented Go
// program, nothing engine-specific.
package trace

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// Summary is the mechanical extraction from one trace; the caller interprets it.
type Summary struct {
	SpanCount int
	Shape     map[string]string // request-shape attribute -> representative value
	FanOut    []SpanStat        // per span-name aggregates, sorted by count desc
}

type SpanStat struct {
	Name         string
	Count        int
	SumMs, MaxMs int64
}

type otlpTrace struct {
	Trace struct {
		ResourceSpans []struct {
			ScopeSpans []struct {
				Spans []otlpSpan `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	} `json:"trace"`
}

type otlpSpan struct {
	Name       string `json:"name"`
	Start      string `json:"startTimeUnixNano"`
	End        string `json:"endTimeUnixNano"`
	Attributes []struct {
		Key   string    `json:"key"`
		Value otlpValue `json:"value"`
	} `json:"attributes"`
}

type otlpValue struct {
	StringValue *string  `json:"stringValue"`
	IntValue    *string  `json:"intValue"` // OTLP encodes int64 as a string
	BoolValue   *bool    `json:"boolValue"`
	DoubleValue *float64 `json:"doubleValue"`
}

func (v otlpValue) String() string {
	switch {
	case v.StringValue != nil:
		return *v.StringValue
	case v.IntValue != nil:
		return *v.IntValue
	case v.BoolValue != nil:
		return strconv.FormatBool(*v.BoolValue)
	case v.DoubleValue != nil:
		return strconv.FormatFloat(*v.DoubleValue, 'g', -1, 64)
	}
	return ""
}

// requestShapeKeys are the attributes that define what a request asked for. Generic OTel
// semantic-convention keys across query-serving systems (search/metrics/SQL/RPC/HTTP).
var requestShapeKeys = map[string]bool{
	"query": true, "db.statement": true, "db.query.text": true,
	"http.route": true, "http.target": true, "url.path": true, "rpc.method": true,
}

// Summarize parses an OTLP/JSON trace and returns its request shape and span fan-out.
func Summarize(b []byte) (*Summary, error) {
	var t otlpTrace
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("parse OTLP: %w", err)
	}
	byName := map[string]*SpanStat{}
	shape := map[string]string{}
	count := 0
	for _, rs := range t.Trace.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			for _, s := range ss.Spans {
				count++
				a := byName[s.Name]
				if a == nil {
					a = &SpanStat{Name: s.Name}
					byName[s.Name] = a
				}
				a.Count++
				ms := durMs(s.Start, s.End)
				a.SumMs += ms
				if ms > a.MaxMs {
					a.MaxMs = ms
				}
				for _, at := range s.Attributes {
					if requestShapeKeys[at.Key] && shape[at.Key] == "" {
						shape[at.Key] = at.Value.String()
					}
				}
			}
		}
	}
	if count == 0 {
		return nil, fmt.Errorf("no spans")
	}
	fan := make([]SpanStat, 0, len(byName))
	for _, a := range byName {
		fan = append(fan, *a)
	}
	sort.Slice(fan, func(i, j int) bool { return fan[i].Count > fan[j].Count })
	return &Summary{SpanCount: count, Shape: shape, FanOut: fan}, nil
}

func durMs(start, end string) int64 {
	s, _ := strconv.ParseInt(start, 10, 64)
	e, _ := strconv.ParseInt(end, 10, 64)
	if s == 0 || e == 0 || e < s {
		return 0
	}
	return (e - s) / 1_000_000
}

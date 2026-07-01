# go-perf-agent JSON schemas

Every JSON artifact go-perf-agent writes under `.go-perf-agent/` has a schema here. These are the
contract between the deterministic engine and the LLM agents. Do not change an artifact's shape
without updating its schema AND the Go type it mirrors - the point is that no stage has to guess.

| artifact | schema | Go type | written by |
|---|---|---|---|
| `hypotheses.json` | `hypothesis.schema.json` | `model.Hypothesis` | HYPOTHESIZE (gpa-analyst + orchestrator) |
| `hotspots.json` | `hotspots.schema.json` | `hotspot.Hotspot` (array) | `hotspots` |
| `scope.json` | `scope.schema.json` | `hotspot.Scope` | `scope`, `target-diff` |
| `diff.json` | `diff.schema.json` | `diff.Meta` | `target-diff` |
| `runs/<id>/verdict.json` | `verdict.schema.json` | `model.Verdict` | `bench verdict`/`baseline`, `critic` |
| `runs/<id>/regression.json` | `regression.schema.json` | `gate.RegressionVerdict` | `bench regression` |
| `telemetry/summary.json` | `telemetry-summary.schema.json` | written by gpa-query-telemetry | COLLECT (gpa-query-telemetry) |

Not schema'd (external formats or not JSON, not ours to define): `traces/*.json` (gcx/Tempo OTLP),
`profiles/*.exemplars.*.json` (gcx pyroscope), `profiles/*.pb.gz` and `*.prof` (binary pprof),
`deployed_version` (plain text), `runs/<id>/{baseline,candidate}.txt` and `benchstat.csv`, `report.md`.

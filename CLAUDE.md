# go-perf-agent - repo guide

LLM-assisted Go performance audit agent. It finds where a Go service is slow from real telemetry,
forms one-change optimization hypotheses, and proves or rejects each with benchmarks. Works on any
Go codebase; developed and tested against Tempo (Tempo is the test case, not baked into the engine).

## Architecture

- A single Go binary (the deterministic engine): collect telemetry, rank hotspots, run the
  interleaved benchstat gate, report. CLI is `alecthomas/kong` (one `Run() error` per command).
- A Claude Code skill + agents (the LLM loop) under `.claude/`: `skills/go-perf-agent/SKILL.md`
  orchestrates; `agents/gpa-*.md` are the specialists (query-telemetry, analyst, validation, critic).
- The two halves connect through JSON files under `.go-perf-agent/` (gitignored), not direct calls.

## Core principles (do not violate)

- Hard numbers decide keep/reject (benchstat), never the model. The LLM only reasons about code.
- Every finding is a hypothesis; a PROVED verdict means "worth shipping behind a flag and verifying
  in prod", not "proven".
- Keep the engine generic: parse only standard formats (OTLP/JSON traces, Go pprof) and match
  generic OTel semantic-convention keys. No Tempo span names / parquet / engine specifics in code.
  "Tempo"/"Pyroscope" appear only as the gcx backends the tool reads from.
- less dependency policy, Don't add deps but also don't reimplement what you get from a dependency — for example we call gcx/pprof to avoid doing what these tools already do.
- The catalog (`catalog/patterns.yaml`) is data. `code` patterns are benchstat-gated; `workload` patterns are advisory (telemetry-detected, not validated by the gate).
- the tool is shim and it's job is to assist LLM agents, and skill is the core loop
- don't overfit to the examples

## Build / test / run

Read [Makefile](./Makefile) for how to build, lint, test and run the project and it's evals.
Read [README.md](./README.md) for more about this project and its details
Read [CONTRIBUTING.md](./CONTRIBUTING.md) for more about how to contribute to this project

## Conventions and things to do when working on this repo.

- Maintain a `PLAN.md` and `DECISIONS.md` file, these files are gitignored but they are working docs, if these don't exist, read the codeba and build these docs, and then keep them current when you change behavior; DECISIONS is append-only.
- Commit messages: short subject, short and focused body. Atomic commits grouped by concern in atomic commits, and only commit with human approval.
- Apply the user's global rules in `~/.claude/CLAUDE.md` (Go style, commit discipline, etc.).
- Use `goimports` to format code, and Go LSP to ensure you write valid Go.
- Always add tests for the code you are adding
- Goal is to keep the code minimal and readable, don't add unnecessary complexity or dependencies when possible.

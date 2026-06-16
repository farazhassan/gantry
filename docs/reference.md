# Gantry Reference

Detailed reference for Gantry's components, conformance suites, eval harness,
testing, and repository layout. For the overview, install steps, quick start,
and core concepts, see the [main README](../README.md).

## Contents

- [Components](#components)
- [Conformance](#conformance)
- [Eval](#eval)
- [Testing](#testing)
- [Project layout](#project-layout)

## Components

Components don't know about phases — convenience `With…` constructors translate
each one into the right middleware on the right phase. Mix and match what you need.

| Component | What it does | Wire it up | Built-ins |
|-----------|--------------|------------|-----------|
| **memory** | Persists & reads conversation history across runs | `memory.WithMemory(a, m)` | `NewInMemoryStore()` |
| **tool** | Capabilities the LLM can invoke, with parallel dispatch | `tool.WithTools(a, parallelism, tools...)` · `tool.WithTool(a, t)` · `tool.WithRegistry(a, reg, parallelism)` | `NewRegistry()` |
| **skill** | Conditional instruction/context blocks injected into the system prompt | `skill.WithSkill(a, s)` | `NewStatic(name, prompt)` |
| **retriever** | Fetches top-`k` docs for RAG and injects them | `retriever.WithRetriever(a, r, k)` | `NewStatic(docs)` |
| **planner** | Decomposes the task into a plan up front | `planner.WithPlanner(a, p)` | `NewLLM(client, rubric)` |
| **critic** | Self-reviews the last response (pass / reject) | `critic.WithCritic(a, c)` | `NewLLM(client, rubric)` |
| **guardrail** | Validates inputs (pre-LLM) and outputs (post-LLM) | `guardrail.WithGuardrail(a, g)` | `NewRegex(pattern, direction)` |
| **limiter** | Caps tokens, cost, and iterations; stops the run when exceeded | `limiter.WithLimiter(a, l)` | `NewBudget(Limits{...})` |
| **compactor** | Trims history to fit a token budget before the LLM call | `compactor.WithCompactor(a, c, budget)` | `NewSlidingWindow(n)` · `NewHeadTail(head, tail)` · `NewSummarizing(client, head, tail)` |
| **humanloop** | Pauses for human approval before tool execution | `humanloop.WithHumanInLoop(a, h)` | `NewAutoApprover()` · `NewAutoDenier(reason)` |
| **checkpointer** | Saves & restores state by id for resume / replay | `checkpointer.WithCheckpointer(a, c, id)` | `NewInMemory()` |

Each built-in is a reference implementation — swap in your own (a Redis
checkpointer, a vector-store retriever, a real guardrail service) by satisfying
the component's interface.

### Putting it together

`examples/e2e` wires **every** component onto a single agent and runs a scripted
scenario end to end:

```go
a, _ := gantry.NewAgent(gantry.WithLLM(scriptedLLM), gantry.WithMaxIterations(8))

memory.WithMemory(a, memory.NewInMemoryStore())

skill.WithSkill(a, skill.NewStatic("careful", "Be careful with numbers and cite the tool you used."))

retriever.WithRetriever(a, retriever.NewStatic(docs), 3)

compactor.WithCompactor(a, compactor.NewSlidingWindow(20), compactor.Budget{})

tool.WithTools(a, 4, calcTool{})

limiter.WithLimiter(a, limiter.NewBudget(limiter.Limits{MaxTokens: 10_000, MaxCostUSD: 1.0}))

guardrail.WithGuardrail(a, guardrail.NewRegex(`(?i)forbidden`, guardrail.DirectionOutput))

critic.WithCritic(a, critic.NewLLM(helperLLM, "Reply PASS if the answer is correct; FAIL otherwise."))

planner.WithPlanner(a, planner.NewLLM(helperLLM, "Break the task into numbered steps."))

humanloop.WithHumanInLoop(a, humanloop.NewAutoApprover())

checkpointer.WithCheckpointer(a, checkpointer.NewInMemory(), "example-run")

state, _ := a.Run(ctx, "what is 2 + 3?")
```

Run it:

```sh
go run ./examples/e2e
```

## Conformance

Writing your own component implementation? The `conformance` package ships
reusable test suites that verify an implementation honors its contract. Drop one
into a `_test.go` and pass a factory:

```go
func TestMyMemory(t *testing.T) {
	conformance.MemorySuite(t, func() memory.Memory {
		return mypkg.NewMemory()
	})
}
```

Suites are provided for every contract: `Memory`, `Tool`, `Checkpointer`,
`Compactor`, `Critic`, `Guardrail`, `HumanInLoop`, `Limiter`, `Planner`,
`Retriever`, `LLMClient`, and `Tracer`.

## Eval

The `eval` package treats agents as black boxes (via an `AgentFactory`) and sweeps
configurations × cases × scorers into an aggregated report.

- **Datasets:** `JSONLDataset` (load from a `.jsonl` file) or `SliceDataset` (in-memory).
- **Scorers:** `ExactMatch`, `Regex`, `Contains`, `Trace`, `Usage`, `Latency`, and
  `LLMJudge` — or implement the `Scorer` interface yourself.
- **Runner:** coordinates the sweep and returns a `Report` with per-case scores and aggregates.
- **MockLLMClient:** `NewMockLLMClient(responses...)` scripts deterministic LLM
  replies so evals (and tests) are reproducible.

## Testing

```sh
go test ./...           # root module (nested modules with their own go.mod are tested separately)
go test -race ./...     # root module with the race detector (what CI runs)
go vet ./...
gofmt -l .              # lists files needing formatting (empty = clean)
```

### Continuous integration

Every push and pull request runs the [CI workflow](../.github/workflows/ci.yml), split
into jobs that double as required status checks for branch protection on `main`:

- **Lint & format** — `gofmt` check, `go vet`, and `staticcheck`.
- **Build** — `go build ./...` on Linux, macOS, and Windows.
- **Test** — `go test -race` with coverage on Go 1.22 and the latest stable Go.
- **Tidy** — `go mod verify` plus a `go mod tidy` no-op check.

Two more workflows complete the pipeline:

- **[Release](../.github/workflows/release.yml)** — triggered by a pushed `v*` tag;
  re-runs the build and tests, then publishes a GitHub Release with auto-generated
  notes (tags with a pre-release suffix like `v0.0.1-beta` are flagged as
  pre-releases).
- **[CodeQL](../.github/workflows/codeql.yml)** — security and quality scanning on
  pushes, PRs, and a weekly schedule.

[Dependabot](../.github/dependabot.yml) keeps Go modules and GitHub Actions versions
up to date.

## Project layout

```
./            Core agent loop (package gantry): phases, middleware, State, and the LLMClient interface
components/   Drop-in capabilities (memory, tool, skill, retriever, planner, critic,
              guardrail, limiter, compactor, humanloop, checkpointer)
conformance/  Reusable test suites that verify implementations satisfy each contract
eval/         Dataset / scorer / runner harness plus a scriptable mock LLM client
examples/     Runnable end-to-end example wiring every component together
docs/         Reference documentation (this file) plus design specs and plans
```

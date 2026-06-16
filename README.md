# Gantry

An agent framework for Go. Gantry gives you a phase-based agent loop with
onion-style (`net/http`-style) middleware at every phase — a small, dependency-free
foundation for prototyping and shipping LLM agents in Go.

[![CI](https://github.com/farazhassan/gantry/actions/workflows/ci.yml/badge.svg)](https://github.com/farazhassan/gantry/actions/workflows/ci.yml)
[![CodeQL](https://github.com/farazhassan/gantry/actions/workflows/codeql.yml/badge.svg)](https://github.com/farazhassan/gantry/actions/workflows/codeql.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/farazhassan/gantry.svg)](https://pkg.go.dev/github.com/farazhassan/gantry)
[![Go 1.22+](https://img.shields.io/badge/go-1.22%2B-00ADD8)](https://go.dev/dl/)
[![License: MIT](https://img.shields.io/badge/license-MIT-green)](LICENSE)
[![Version](https://img.shields.io/badge/version-0.0.1%20beta-orange)](https://github.com/farazhassan/gantry/releases)

> **Status: v0.0.1 (Beta).** The core loop and component contracts are in place,
> but the public API may still change ahead of a v1.0 release.

## Why Gantry

- **Tiny core, zero vendor lock-in.** The agent core depends only on the Go
  standard library. You supply one interface — `LLMClient` — and wire in
  Anthropic, OpenAI, a local model, or a mock. No SDK is imported for you.
- **Middleware all the way down.** Every stage of the loop is an onion of
  middleware you control. Retries, caching, timing, short-circuiting, and state
  mutation are all just middleware — the same pattern you already know from HTTP.
- **Batteries-included components.** Memory, tools, skills, retrieval (RAG),
  planning, self-critique, guardrails, rate/budget limiting, checkpointing,
  human-in-the-loop, and context compaction all ship as drop-in components.
- **Built for confidence.** Every component is defined by a contract, and Gantry
  ships a reusable **conformance** test suite so your own implementations can
  prove they honor that contract. An **eval** harness sweeps configs × cases ×
  scorers and produces machine- and human-readable reports.

## Install

```sh
go get github.com/farazhassan/gantry
```

Requires **Go 1.22+**.

## Quick start

Bring your own `LLMClient` — that's the only required dependency. Here's a
complete, runnable agent backed by a trivial echo "model":

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/farazhassan/gantry"
)

// echoLLM is a stand-in LLMClient. Swap in an Anthropic/OpenAI adapter for real use.
type echoLLM struct{}

func (echoLLM) Generate(_ context.Context, req gantry.LLMRequest) (gantry.LLMResponse, error) {
	last := req.Messages[len(req.Messages)-1].Content
	return gantry.LLMResponse{
		Content:    "you said: " + last,
		StopReason: gantry.StopReasonEnd,
	}, nil
}

func main() {
	agent, err := gantry.NewAgent(gantry.WithLLM(echoLLM{}))
	if err != nil {
		log.Fatal(err)
	}

	state, err := agent.Run(context.Background(), "hello")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(state.FinalOutput) // you said: hello
	fmt.Println(state.DoneReason)  // no_tool_calls
}
```

## Core concepts

### The phase loop

The agent core is a phase machine. The default loop runs `PhaseStart` once at the
top, repeats the inner phases until the run is done, then runs `PhaseEnd` once:

```
PhaseStart  →  ┌─ PhaseAssembleContext ─┐
               │  PhaseLLMCall           │
               │  PhasePostLLM           │ ← repeat until state.Done
               │  PhaseToolExec          │   (or MaxIterations reached)
               └─ PhaseObserve        ───┘
            →  PhaseEnd
```

Built-in inner handlers cover the essentials — LLM invocation, tool-call parsing,
and folding tool results back into the transcript. Everything else is contributed
by middleware. You can also insert your own phases with
`agent.RegisterPhase(phase, position, anchor)`.

### Middleware — the onion model

```go
type Handler    func(ctx context.Context, state *State) error
type Middleware func(next Handler) Handler
```

Each middleware decides when (or whether) to call `next`. Registration order is
**innermost-first**: given middleware `[A, B, C]` and inner handler `I`, the
composed chain is `C(B(A(I)))`, so execution flows from `C` inward to `I` and back
out. Use `agent.Use` for anonymous middleware, or `UseNamed` / `UseBefore` /
`UseAfter` when ordering matters.

```go
agent.Use(gantry.PhaseLLMCall, retryMiddleware)
agent.Use(gantry.PhaseAssembleContext, injectContextMiddleware)
```

### State

A single mutable `*gantry.State` flows through every middleware. It carries the
input and task, the assembled context (system prompt, messages, tools, retrieved
docs, plan), loop state, termination info, and observability (trace + usage). A
`Meta map[string]any` escape hatch lets middleware pass data to one another
(namespace your keys to avoid collisions).

### Termination

`Run` always returns a non-nil `*State` — even on error — so you can always
inspect `state.DoneReason` and the trace.

- **Resource / normal stops** (`no_tool_calls`, `max_iterations`,
  `budget_exceeded`) set `state.Done` and return a **nil** error.
- **Active blocks / aborts** (`guardrail_blocked`, `human_aborted`) set
  `state.Done` **and** return a sentinel error — use `errors.Is` with
  `gantry.ErrGuardrailBlocked` / `gantry.ErrHumanAborted` to branch.

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

## Examples

Start with the focused examples below — each teaches exactly one idea and runs
under `go test` with no API keys. `examples/e2e` (above) is the "everything
together" reference once the pieces click.

| Example | One concept it teaches | Run |
|---------|------------------------|-----|
| **minimal** | The smallest agent: the loop, `FinalOutput`, `DoneNoToolCalls` | `go run ./examples/minimal` |
| **tools** | Defining and dispatching a tool (`PhaseToolExec → PhaseObserve`) | `go run ./examples/tools` |
| **middleware** | The onion model: logging + retry middleware on `PhaseLLMCall` | `go run ./examples/middleware` |
| **rag** | Retrieval-augmented context via `retriever.WithRetriever` | `go run ./examples/rag` |
| **guardrail** | Termination: active block (sentinel error) vs. budget stop (nil error) | `go run ./examples/guardrail` |
| **checkpoint** | Persisting and restoring run state by id | `go run ./examples/checkpoint` |
| **streaming** | Streaming a whole run as JSON events over SSE via `RunStream` | `go run ./examples/streaming` |
| **e2e** | Every component wired onto one agent | `go run ./examples/e2e` |

## Eval

The `eval` package treats agents as black boxes (via an `AgentFactory`) and sweeps
configurations × cases × scorers into an aggregated report.

- **Datasets:** `JSONLDataset` (load from a `.jsonl` file) or `SliceDataset` (in-memory).
- **Scorers:** `ExactMatch`, `Regex`, `Contains`, `Trace`, `Usage`, `Latency`, and
  `LLMJudge` — or implement the `Scorer` interface yourself.
- **Runner:** coordinates the sweep and returns a `Report` with per-case scores and aggregates.
- **MockLLMClient:** `NewMockLLMClient(responses...)` scripts deterministic LLM
  replies so evals (and tests) are reproducible.

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

## Project layout

```
./            Core agent loop (package gantry): phases, middleware, State, and the LLMClient interface
components/   Drop-in capabilities (memory, tool, skill, retriever, planner, critic,
              guardrail, limiter, compactor, humanloop, checkpointer)
conformance/  Reusable test suites that verify implementations satisfy each contract
eval/         Dataset / scorer / runner harness plus a scriptable mock LLM client
examples/     Runnable end-to-end example wiring every component together
docs/         Design specs and implementation plans
```

## Testing

```sh
go test ./...           # full suite
go test -race ./...     # with the race detector (what CI runs)
go vet ./...
gofmt -l .              # lists files needing formatting (empty = clean)
```

### Continuous integration

Every push and pull request runs the [CI workflow](.github/workflows/ci.yml), split
into jobs that double as required status checks for branch protection on `main`:

- **Lint & format** — `gofmt` check, `go vet`, and `staticcheck`.
- **Build** — `go build ./...` on Linux, macOS, and Windows.
- **Test** — `go test -race` with coverage on Go 1.22 and the latest stable Go.
- **Tidy** — `go mod verify` plus a `go mod tidy` no-op check.

Two more workflows complete the pipeline:

- **[Release](.github/workflows/release.yml)** — triggered by a pushed `v*` tag;
  re-runs the build and tests, then publishes a GitHub Release with auto-generated
  notes (tags with a pre-release suffix like `v0.0.1-beta` are flagged as
  pre-releases).
- **[CodeQL](.github/workflows/codeql.yml)** — security and quality scanning on
  pushes, PRs, and a weekly schedule.

[Dependabot](.github/dependabot.yml) keeps Go modules and GitHub Actions versions
up to date.

## Roadmap

The core loop and component contracts are in place; the items below are planned
built-ins and adapters. More ideas, feature requests or contributions toward any of these are especially welcome
— see [Contributing](#contributing).

**LLM clients / agents**
- [ ] OpenAI adapter
- [ ] Anthropic adapter
- [ ] Llama adapter
- [ ] Others

**Memory**
- [ ] File-backed store
- [ ] Vector store

**Observability**
- [ ] Langfuse tracer
- [ ] Custom tracer hooks
- [ ] Logging to terminal
- [ ] Logging to file

**Streaming**
- [x] Optional `StreamingLLMClient` + `RunStream` whole-run event stream

## Contributing

Contributions are welcome! Bug fixes, new components, LLM adapters, and docs
improvements are all appreciated.

- **Found a bug or have an idea?** Open an issue to discuss it first.
- **Building a component?** Implement the relevant interface, validate it against
  the matching [conformance](#conformance) suite, and add tests.
- **Before opening a PR:** run `go vet ./...` and `go test -race ./...` and make
  sure everything passes — that's what CI checks.
- Picking up something from the [Roadmap](#roadmap) is a great place to start.

By contributing, you agree that your contributions will be licensed under the
project's [MIT License](LICENSE).

## License

Gantry is released under the [MIT License](LICENSE) — free to use, modify, and
distribute.

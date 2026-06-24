# Gantry

**A tiny, testable, Go-native agent runtime for teams that want control, conformance, and no framework lock-ins.**

Gantry gives you a phase-based agent loop with onion-style (`net/http`-style)
middleware at every phase — a small, dependency-free foundation for prototyping
and shipping LLM agents in Go.

[![CI](https://github.com/farazhassan/gantry/actions/workflows/ci.yml/badge.svg)](https://github.com/farazhassan/gantry/actions/workflows/ci.yml)
[![CodeQL](https://github.com/farazhassan/gantry/actions/workflows/codeql.yml/badge.svg)](https://github.com/farazhassan/gantry/actions/workflows/codeql.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/farazhassan/gantry.svg)](https://pkg.go.dev/github.com/farazhassan/gantry)
[![Go 1.22+](https://img.shields.io/badge/go-1.22%2B-00ADD8)](https://go.dev/dl/)
[![License: MIT](https://img.shields.io/badge/license-MIT-green)](LICENSE)
[![Version](https://img.shields.io/badge/version-0.0.1%20beta-orange)](https://github.com/farazhassan/gantry/releases)

> **Status: v0.0.1 (Beta).** The core loop and component contracts are in place,
> but the public API may still change ahead of a v1.0 release.

## Why Gantry

- **Control.** Every stage of the loop is an onion of middleware you compose
  with ordinary Go — retries, caching, timing, short-circuiting, and state
  mutation are all just middleware. No DSL, no hidden control flow; you decide
  what runs and when.
- **Conformance.** Every component is defined by a contract, and Gantry ships a
  reusable **conformance** test suite so your own implementations can prove they
  honor that contract.
- **Testability.** Units are plain `func(ctx, *State) error` handlers; a mock
  LLM client and a black-box **eval** harness (configs × cases × scorers) let
  you test agents like normal Go code, with no API keys.
- **No lock-in.** The agent core depends only on the Go standard library. You
  supply one interface — `LLMClient` — and wire in Anthropic, OpenAI, a local
  model, or a mock. Adapters and components are opt-in, never load-bearing.

## Why a phase loop + middleware?

Gantry runs a fixed, inspectable sequence of phases every turn — assemble
context, call the LLM, post-process, run tools, observe — so you know exactly
what runs, and in what order, before anything executes. There's no graph to wire
and no edges to trace.

Behavior attaches as middleware: each phase is a chain of
`func(next Handler) Handler`, and you insert logic anywhere with `Use`,
`UseBefore`, or `UseAfter` a named anchor — no rewiring, no rebuilding a graph.

Graph/DAG and workflow-builder frameworks express agent logic as nodes and edges
in a bespoke abstraction. It's powerful, but you end up debugging the graph
rather than your code, and each node is awkward to exercise on its own. Gantry's
units are ordinary Go functions: unit-test one in a line, compose them by plain
wrapping, and read the whole loop top to bottom. The payoff is the control and
testability the wedge promises, made concrete.

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

Batteries-included, opt-in capabilities that attach as middleware via convenience
`With…` constructors — mix and match what you need:

**memory** · **tool** · **skill** · **retriever** · **planner** · **critic** ·
**guardrail** · **limiter** · **compactor** · **humanloop** · **checkpointer**

Each ships a reference built-in (swap in your own by satisfying the component's
interface). Full reference table, built-ins, and an end-to-end example wiring
every component onto one agent → **[docs/reference.md](docs/reference.md#components)**.

For keyed, durable multi-turn conversations — how state is shared across every
message in a session — see the **[sessions guide](docs/sessions.md)**.

## Examples

Start with the focused examples below — each teaches exactly one idea and runs
under `go test` with no API keys. `examples/e2e` is the "everything together"
reference once the pieces click — its full wiring is in
[docs/reference.md](docs/reference.md#components).

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

## Testing

Run the root module test suite with `go test ./...` (CI runs `go test -race ./...`).
This repo also contains nested Go modules (e.g. `gantry/mcp`, `examples/assistant`); run `go test ./...` inside them as needed.
Conformance suites, the eval harness, and the CI/release pipeline are documented in **[docs/reference.md](docs/reference.md)**.

## Roadmap

The core loop and component contracts are in place; planned built-ins, adapters,
and capabilities — grouped by milestone — live in **[docs/roadmap.md](docs/roadmap.md)**.
Contributions toward any of them are especially welcome.

## Contributing

Contributions are welcome — bug fixes, new components, LLM adapters, examples, and
docs. See **[CONTRIBUTING.md](CONTRIBUTING.md)** for development setup, component
and commit conventions, and the PR checklist.

## License

Gantry is released under the [MIT License](LICENSE) — free to use, modify, and
distribute.

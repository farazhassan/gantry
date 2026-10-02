# Basic middleware

In Express you attach handlers to routes with `app.METHOD(PATH, HANDLER)`. In
Gantry you attach **middleware** to **phases** of the agent loop:

```go
agent.Use(PHASE, MIDDLEWARE)
```

- `agent` is a `*gantry.Agent`.
- `PHASE` is a step of the agent loop, such as `gantry.PhaseLLMCall`.
- `MIDDLEWARE` is a function that wraps the phase.

## The phases

Every call to `agent.Run` walks the same fixed sequence of phases. These are
the "routes" of a Gantry agent:

```text
PhaseStart  →  ┌─ PhaseAssembleContext ─┐
               │  PhaseLLMCall           │
               │  PhasePostLLM           │ ← repeat until state.Done
               │  PhaseToolExec          │   (or MaxIterations reached)
               └─ PhaseObserve        ───┘
            →  PhaseEnd
```

`PhaseStart` runs once, the inner phases repeat until the run is done, and
`PhaseEnd` runs once at the end.

## Handlers and middleware

Two types do all the work:

```go
type Handler    func(ctx context.Context, state *State) error
type Middleware func(next Handler) Handler
```

A middleware receives the `next` handler and returns a new one. It can run code
before `next`, after `next`, change `state`, or skip `next` entirely.

### Run code around a phase

This middleware times each LLM call. Everything before `next` runs on the way
in; everything after runs on the way out:

```go
// timing logs how long each LLM call takes.
func timing(next gantry.Handler) gantry.Handler {
	return func(ctx context.Context, state *gantry.State) error {
		start := time.Now()
		err := next(ctx, state)
		log.Printf("llm call took %s", time.Since(start))
		return err
	}
}
```

### Change the state

`*gantry.State` carries everything about the run — the input, the system
prompt, messages, tools, and the result. Middleware changes the run by changing
the state. This one sets the system prompt while the context is assembled:

```go
// pirate sets the system prompt before the context reaches the LLM.
func pirate(next gantry.Handler) gantry.Handler {
	return func(ctx context.Context, state *gantry.State) error {
		state.System = "You are a pirate. Always answer in pirate speak."
		return next(ctx, state)
	}
}
```

### End a phase early

A middleware that doesn't call `next` stops the phase right there. This one
answers by itself when the input mentions passwords, so the LLM is never
called:

```go
// blockPasswords answers itself, without calling the LLM, when the
// input mentions passwords.
func blockPasswords(next gantry.Handler) gantry.Handler {
	return func(ctx context.Context, state *gantry.State) error {
		if strings.Contains(strings.ToLower(state.Input), "password") {
			state.LastResponse = &gantry.LLMResponse{
				Content:    "Sorry, I can't help with passwords.",
				StopReason: gantry.StopReasonEnd,
			}
			return nil // next is never called, so no LLM request is made
		}
		return next(ctx, state)
	}
}
```

## Putting it together

Here is the [Hello agent](hello-agent.md) with all three middleware attached:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/llm/openrouter"
)

// timing logs how long each LLM call takes.
func timing(next gantry.Handler) gantry.Handler {
	return func(ctx context.Context, state *gantry.State) error {
		start := time.Now()
		err := next(ctx, state)
		log.Printf("llm call took %s", time.Since(start))
		return err
	}
}

// pirate sets the system prompt before the context reaches the LLM.
func pirate(next gantry.Handler) gantry.Handler {
	return func(ctx context.Context, state *gantry.State) error {
		state.System = "You are a pirate. Always answer in pirate speak."
		return next(ctx, state)
	}
}

// blockPasswords answers itself, without calling the LLM, when the
// input mentions passwords.
func blockPasswords(next gantry.Handler) gantry.Handler {
	return func(ctx context.Context, state *gantry.State) error {
		if strings.Contains(strings.ToLower(state.Input), "password") {
			state.LastResponse = &gantry.LLMResponse{
				Content:    "Sorry, I can't help with passwords.",
				StopReason: gantry.StopReasonEnd,
			}
			return nil // next is never called, so no LLM request is made
		}
		return next(ctx, state)
	}
}

func main() {
	llm := openrouter.New("deepseek/deepseek-v4-flash")

	agent, err := gantry.NewAgent(gantry.WithLLM(llm))
	if err != nil {
		log.Fatal(err)
	}

	agent.Use(gantry.PhaseAssembleContext, pirate)
	agent.Use(gantry.PhaseLLMCall, timing)
	agent.Use(gantry.PhaseLLMCall, blockPasswords)

	for _, input := range []string{"What is Go?", "What's my password?"} {
		state, err := agent.Run(context.Background(), input)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("> %s\n%s\n\n", input, state.FinalOutput)
	}
}
```

Run it with `go run .`. The first question gets a pirate answer and a timing
log line. The second is answered by `blockPasswords` — and there's no timing
line, because the LLM call never happened.

## Middleware order

Middleware on the same phase wraps like an onion. Registration order is
**innermost-first**: for middleware registered as `A`, `B`, `C` around the
phase's built-in handler `I`, the chain is `C(B(A(I)))`. Execution flows from
`C` inward to `I` and back out.

In the program above, `blockPasswords` was registered after `timing`, so it is
the outer layer: it runs first and can stop the call before `timing` ever
starts.

When order matters across packages, name your middleware with `UseNamed` and
place others relative to it with `UseBefore` and `UseAfter`.

**Next:** [Adding a tool](adding-a-tool.md)

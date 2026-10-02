# FAQ

## How is Gantry different from LangChain or graph frameworks?

Graph and workflow frameworks describe an agent as nodes and edges in their own
abstraction. Gantry runs a fixed, readable sequence of
[phases](basic-middleware.md#the-phases) every turn, and you add behavior as
ordinary Go middleware. There's no graph to debug: each unit is a plain function
you can unit-test in a line, and you can read the whole loop top to bottom.

## Which LLM providers are supported?

Gantry ships adapters for OpenRouter, Ollama, OpenAI, and Anthropic under
`components/llm/`. Any other provider works if you implement the
one-method `gantry.LLMClient` interface:

```go
Generate(ctx context.Context, req gantry.LLMRequest) (gantry.LLMResponse, error)
```

## How do I test an agent without an API key?

Use the scripted mock client from the `eval` package. It replays the responses
you give it, in order:

```go
llm := eval.NewMockLLMClient(gantry.LLMResponse{
	Content:    "Hello!",
	StopReason: gantry.StopReasonEnd,
})
```

Every program in [`examples/`](examples.md) is tested this way. For scoring
agents across many cases, see the
[eval harness](../resources/reference.md#eval).

## How does a run stop, and what does `DoneReason` mean?

`Run` always returns a non-nil `*State`, so you can check
`state.DoneReason`. Stops fall into two groups:

- **Normal and resource stops** — `no_tool_calls`, `max_iterations`,
  `budget_exceeded` — return a **nil** error.
- **Blocks and aborts** — `guardrail_blocked`, `human_aborted` — return a
  sentinel error. Branch on them with `errors.Is(err, gantry.ErrGuardrailBlocked)`
  or `errors.Is(err, gantry.ErrHumanAborted)`.

Cap the number of loop iterations with `gantry.WithMaxIterations(n)`.

## Where does conversation history live between calls?

An agent is stateless across `Run` calls. For multi-turn conversations, use a
[session](../guide/sessions.md): it stores each conversation in a checkpointer,
keyed by session id.

## How do I add retries or caching?

Write middleware on `PhaseLLMCall`. A retry middleware calls `next` again when
it returns an error; a cache middleware sets `state.LastResponse` and returns
without calling `next`, exactly like `blockPasswords` in
[Basic middleware](basic-middleware.md#end-a-phase-early).

## Is Gantry ready for production?

Gantry is in **beta**. The core loop and component contracts are in place, but
the public API may still change before v1.0. Pin a version in `go.mod` and read
the release notes when you upgrade.

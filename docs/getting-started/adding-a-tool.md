# Adding a tool

Tools are how an agent acts — calling an API, querying a database, or doing
math it shouldn't do in its head. This page gives the
[Hello agent](hello-agent.md) a calculator.

A tool implements two methods: `Definition`, which describes it to the model,
and `Invoke`, which runs it with the model's JSON arguments.

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/llm/openrouter"
	"github.com/farazhassan/gantry/components/tool"
)

// calcTool adds two integers.
type calcTool struct{}

func (calcTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{
		Name:        "calc",
		Description: "Adds two integers a and b.",
		Schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"a": {"type": "integer"},
				"b": {"type": "integer"}
			},
			"required": ["a", "b"]
		}`),
	}
}

func (calcTool) Invoke(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	var args struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	if err := json.Unmarshal(in, &args); err != nil {
		return nil, err
	}
	return json.Marshal(args.A + args.B)
}

func main() {
	llm := openrouter.New("deepseek/deepseek-v4-flash")

	agent, err := gantry.NewAgent(gantry.WithLLM(llm))
	if err != nil {
		log.Fatal(err)
	}

	if err := agent.With(tool.FromTools(1, calcTool{})); err != nil {
		log.Fatal(err)
	}

	state, err := agent.Run(context.Background(), "What is 2 + 3? Use the calc tool.")
	if err != nil {
		log.Fatal(err)
	}

	for _, m := range state.Messages {
		fmt.Printf("%-9s %s\n", m.Role, m.Content)
	}
	fmt.Println("done:", state.DoneReason)
}
```

Run it with `go run .` and you'll see the whole conversation (wording varies):

```text
user      What is 2 + 3? Use the calc tool.
assistant
tool      5
assistant 2 + 3 = 5.
done: no_tool_calls
```

## How the loop ran

This time the agent loop went around **twice**:

1. **First pass.** The model saw the `calc` tool and, instead of answering,
   asked to call it with `{"a": 2, "b": 3}` — that's the assistant message,
   usually with no text. `PhaseToolExec` ran `calcTool.Invoke`, and `PhaseObserve` added the
   result (`5`) to the conversation as a `tool` message.
2. **Second pass.** The model saw the tool result and answered in plain text.
   With no more tool calls to make, the run stopped with `no_tool_calls`.

## Tools are middleware too

`tool.FromTools(1, calcTool{})` returns a **component** — a packaged bundle of
middleware. `agent.With` installs it on the right phases for you: it advertises
the tools once when the run starts (`PhaseStart`) and dispatches calls in
`PhaseToolExec`. The `1` is the parallelism: how many tool calls may run at
once.

Every Gantry capability — memory, retrieval, guardrails, budgets — works the
same way. See the [component reference](../resources/reference.md#components)
for the full list.

**Next:** [More examples](examples.md)

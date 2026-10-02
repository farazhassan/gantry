# Hello agent

Here's the smallest useful Gantry agent: it sends one prompt to a real model
and prints the answer. Save it as `main.go` in the `myagent` directory you
created in [Installing](installing.md):

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/llm/openrouter"
)

func main() {
	llm := openrouter.New("deepseek/deepseek-v4-flash")

	agent, err := gantry.NewAgent(gantry.WithLLM(llm))
	if err != nil {
		log.Fatal(err)
	}

	state, err := agent.Run(context.Background(), "Say hello in one sentence.")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(state.FinalOutput)
	fmt.Println("done:", state.DoneReason)
}
```

Run it:

```sh
go run .
```

You'll see something like this (the model's wording will vary):

```text
Hello! It's great to meet you.
done: no_tool_calls
```

## What just happened

- `openrouter.New` creates an LLM client for the `deepseek/deepseek-v4-flash`
  model. It reads your key from `OPENROUTER_API_KEY` and panics if none is set —
  a missing key is a wiring mistake, so it fails loudly at startup.
- `gantry.NewAgent` builds an agent. `WithLLM` is the only required option.
- `agent.Run` drives the agent loop for one input. It returns a `*gantry.State`
  — the single value that flows through every step of the run. `Run` always
  returns a non-nil state, even alongside an error, so you can inspect what
  happened.
- `state.FinalOutput` holds the model's answer. `state.DoneReason` says why the
  run stopped: `no_tool_calls` means the model answered without asking for a
  tool, so there was nothing left to do.

!!! tip "Changing the model"
    OpenRouter model ids are namespaced by provider. Swap the string — for
    example `"deepseek/deepseek-v4-pro"` — to use a different model. Nothing
    else changes.

**Next:** [Basic middleware](basic-middleware.md)

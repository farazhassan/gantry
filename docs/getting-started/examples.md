# More examples

The [`examples/`](https://github.com/farazhassan/gantry/tree/main/examples)
directory has a focused program for each idea. Clone the repository and run
them from its root:

```sh
git clone https://github.com/farazhassan/gantry
cd gantry
go run ./examples/minimal
```

!!! note "Most examples run without an API key"
    Unlike the Getting started pages, most examples use Gantry's scripted mock
    LLM (or a hand-written fake) so they run offline and double as tests
    (`go test ./examples/...`). The **Needs** column calls out the ones that
    talk to real services.

## Basics

| Example | What it shows | Needs |
|---------|---------------|-------|
| [minimal](https://github.com/farazhassan/gantry/tree/main/examples/minimal) | The smallest agent: the loop, `FinalOutput`, `DoneNoToolCalls` | — |
| [tools](https://github.com/farazhassan/gantry/tree/main/examples/tools) | Defining and dispatching a tool | — |
| [middleware](https://github.com/farazhassan/gantry/tree/main/examples/middleware) | Logging and retry middleware on `PhaseLLMCall` | — |
| [guardrail](https://github.com/farazhassan/gantry/tree/main/examples/guardrail) | Blocked runs (error) vs. budget stops (no error) | — |
| [e2e](https://github.com/farazhassan/gantry/tree/main/examples/e2e) | Every core component wired onto one agent | — |

## Capabilities

| Example | What it shows | Needs |
|---------|---------------|-------|
| [rag](https://github.com/farazhassan/gantry/tree/main/examples/rag) | Retrieval-augmented generation with `retriever.New` | — |
| [session](https://github.com/farazhassan/gantry/tree/main/examples/session) | One agent across many turns, keyed by session id | — |
| [checkpoint](https://github.com/farazhassan/gantry/tree/main/examples/checkpoint) | Saving and restoring run state | — |
| [checkpoint-resume](https://github.com/farazhassan/gantry/tree/main/examples/checkpoint-resume) | Crash recovery with a lease and `ResumeLocked` | — |
| [handoff](https://github.com/farazhassan/gantry/tree/main/examples/handoff) | A router agent handing a conversation to a specialist | — |
| [subagent](https://github.com/farazhassan/gantry/tree/main/examples/subagent) | A coordinator calling specialist sub-agents | — |
| [task-lifecycle](https://github.com/farazhassan/gantry/tree/main/examples/task-lifecycle) | The full task lifecycle, deterministically | — |
| [task-lifecycle-live](https://github.com/farazhassan/gantry/tree/main/examples/task-lifecycle-live) | The same lifecycle against a live model | Ollama |
| [qdrant](https://github.com/farazhassan/gantry/tree/main/examples/qdrant) | Semantic retrieval backed by Qdrant | Qdrant, OpenAI-compatible API |

## UI and streaming

| Example | What it shows | Needs |
|---------|---------------|-------|
| [streaming](https://github.com/farazhassan/gantry/tree/main/examples/streaming) | A whole run streamed as JSON events over SSE | — |
| [agui](https://github.com/farazhassan/gantry/tree/main/examples/agui) | Serving an agent over the AG-UI protocol | Ollama (or swap in OpenAI/Anthropic) |
| [agui-copilotkit](https://github.com/farazhassan/gantry/tree/main/examples/agui-copilotkit) | AG-UI with CopilotKit frontend actions | Ollama (or swap in OpenAI/Anthropic); Node.js for the frontend |
| [vercelai](https://github.com/farazhassan/gantry/tree/main/examples/vercelai) | Serving an agent to the Vercel AI SDK | Ollama (or swap in OpenAI/Anthropic) |
| [assistant](https://github.com/farazhassan/gantry/tree/main/examples/assistant) | A terminal assistant driving MCP servers | Ollama, Node.js (`npx`), `uv`; own Go module |

## Observability

| Example | What it shows | Needs |
|---------|---------------|-------|
| [langfuse](https://github.com/farazhassan/gantry/tree/main/examples/langfuse) | Sending traces to Langfuse | Langfuse keys |

`assistant` is its own Go module, so run it from inside its directory. Examples
that need external services or ship a frontend have their own README with setup
steps.

**Next:** [FAQ](faq.md)

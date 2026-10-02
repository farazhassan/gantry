# Gantry Roadmap

The core loop and component contracts are in place; the items below are planned
built-ins, adapters, and capabilities. Contributions toward any of these are
especially welcome — see [CONTRIBUTING.md](https://github.com/farazhassan/gantry/blob/main/CONTRIBUTING.md).

Items are grouped by milestone, then by theme. Milestones express rough priority,
not committed dates.

## Shipped

- **LLM clients** — OpenAI adapter · Anthropic adapter
- **Streaming** — `StreamingLLMClient` + `RunStream` whole-run event stream
- **Observability** — Langfuse tracer
- **UI** — AG-UI protocol support
- **Checkpointing** — mid-run checkpoint hooks (`checkpointer.New`'s
  `extraPhases`) and a distributed `Lease` primitive (Redis + etcd) for
  safe multi-worker resume — see `examples/checkpoint-resume`
- **Orchestration** — Tasks: durable, plan-driven work items run across many
  agent runs, with per-session queues, spawning, scheduling, and headless
  dispatch (`task`, `taskmanager`) — see the [tasks guide](../guide/task-management.md)
  and `examples/task-lifecycle`

## Toward v0.1

- **Observability** — OpenTelemetry tracer support · custom tracer hooks ·
  logging to terminal and file
- **Cost & limits** — `TokenCostCalculator`: token and cost accounting for the
  limiter and traces
- **Memory** — file-backed store · vector memory adapters

## Toward v1.0

- **Guardrails** — HarmfulContent · OutOfBudget
- **Tools** — internal, production-ready tools
- **Orchestration** — durable Task backends (the `TaskStore`, `MetaStore`,
  and queue stores ship in-memory only) · handoff inside task-driven runs ·
  support for adding subagents
- **Examples** — a production-ready, runnable end-to-end demo with a frontend
  component

## Exploring / later

- **LLM clients** — Llama adapter · other providers
- **UI** — A2UI: emit [A2UI](https://a2ui.org/) declarative UI descriptions so
  agents can drive rich, cross-platform interfaces (complements the shipped
  AG-UI event streaming)

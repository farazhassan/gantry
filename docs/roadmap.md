# Gantry Roadmap

The core loop and component contracts are in place; the items below are planned
built-ins, adapters, and capabilities. Contributions toward any of these are
especially welcome — see [CONTRIBUTING.md](../CONTRIBUTING.md).

Items are grouped by milestone, then by theme. Milestones express rough priority,
not committed dates.

## Shipped

- **LLM clients** — OpenAI adapter · Anthropic adapter
- **Streaming** — `StreamingLLMClient` + `RunStream` whole-run event stream
- **Observability** — Langfuse tracer
- **UI** — AG-UI protocol support

## Toward v0.1

- **Observability** — OpenTelemetry tracer support · custom tracer hooks ·
  logging to terminal and file
- **Cost & limits** — `TokenCostCalculator`: token and cost accounting for the
  limiter and traces
- **Memory** — file-backed store · vector memory adapters

## Toward v1.0

- **Guardrails** — HarmfulContent · OutOfBudget
- **Tools** — internal, production-ready tools
- **Orchestration** — support for adding Tasks ([design](task-management.md)) ·
  support for adding subagents
- **Examples** — a production-ready, runnable end-to-end demo with a frontend
  component

## Exploring / later

- **LLM clients** — Llama adapter · other providers
- **UI** — A2UI: emit [A2UI](https://a2ui.org/) declarative UI descriptions so
  agents can drive rich, cross-platform interfaces (complements the shipped
  AG-UI event streaming)

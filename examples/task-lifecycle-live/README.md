# Gantry Task Lifecycle Example (Live LLM)

The live-LLM counterpart to [`../task-lifecycle`](../task-lifecycle), which runs
the same lifecycle deterministically against scripted mock LLMs. This example
wires the **same task stack** — agent, `create_task`/`spawn_session` server
tools, the `ask_user` client tool, a critic verifier, in-memory stores, the
driver/manager, and the real `Dispatcher` — but drives it with live
[Ollama](https://ollama.com) clients (one for the agent, one for the critic).

Because a live model freewheels, this example **observes and prints whatever the
model actually does** — it does not assume the scripted narrative (spawn,
reject-then-revise, park) and ships **no automated test**. The deterministic
`../task-lifecycle` example remains the tested artifact.

## Prerequisites

- **Go 1.25+**
- **Ollama** running locally with a tool-capable model pulled, e.g.:

  ```bash
  ollama pull llama3.1
  ```

## Run

```bash
cd examples/task-lifecycle-live
go run . --model llama3.1
```

Flags (all optional):

- `--model` (env `TASK_LIFECYCLE_MODEL`, default `llama3.1`) — Ollama model used
  for both the agent and the critic.
- `--ollama-url` (env `OLLAMA_URL`) — Ollama base URL; empty uses the Ollama
  default.
- `--dispatch-timeout` (default `30s`) — how long to run the headless Dispatcher
  before stopping.

## What it shows

Same wiring as the mock example, but live:

- The draft task is started and driven; its final status and critic-rejection
  count are printed.
- A real `Dispatcher` drains the `ReadyQueue` headlessly for `--dispatch-timeout`.
  If the model spawns a detached session (`spawn_session`) that later asks a
  question (`ask_user`), the task parks at `awaiting_input` and the notifier
  prints the question.

Output varies run to run — whether the model spawns tasks, rejects then revises,
or parks depends entirely on the model. To see the lifecycle demonstrated
deterministically, run [`../task-lifecycle`](../task-lifecycle) instead.

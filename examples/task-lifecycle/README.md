# Gantry Task Lifecycle Example

A self-contained, deterministic program that exercises the **full Gantry task
lifecycle** in one narrative — drafting a v2 release announcement. It wires the
real task components against fully scripted mock LLMs, so the run makes no live
API calls and produces the same result every time.

This is a teaching artifact: it adds no framework code. Everything it touches
lives in `task/`, `taskmanager/`, and `components/`.

## Run

```bash
cd examples/task-lifecycle
go run .
```

Expected output:

```
draft task     : done after 1 critic rejection(s) (reject -> revise -> accept)
proofread task : done (same-session FIFO follow-on)
detached task  : awaiting_input (driven headless by the Dispatcher)
notifier fired : session "schedule-posts" asked: "Which timezone should the posts target?"
```

## What it demonstrates

One story touches every stage of the task layer:

- **Status machine** — a task moves `pending → active → awaiting_input → done`.
- **Critic gate** — the first draft has no call-to-action and is rejected; the
  driver feeds the critique back, the agent revises with a CTA, and the critic
  accepts. `ConsecutiveRejections` ticks to 1, then resets on accept.
- **Same-session spawning (`create_task`)** — a "proofread the draft" follow-on
  lands on the session's FIFO queue and runs to completion within the same
  `StartTask` drive.
- **Cross-session spawning (`spawn_session`)** — an unrelated "schedule the
  launch social posts" session is created detached and enqueued on the
  `ReadyQueue`.
- **Headless driving** — a real `Dispatcher` drains the `ReadyQueue` and drives
  the detached session with no human attached. Its agent calls `ask_user`, the
  task parks at `awaiting_input`, and the `WithNotifier` callback fires with the
  parked task.

## How it works

`RunExample(ctx) (*Result, error)` wires the components and returns the
observable milestones; `main` prints them.

- **One agent, two scripted mock LLMs.** The agent and the critic each get their
  own `eval.NewMockLLMClientFromScript`, so their turns never interleave. The
  critic runs through the real `critic.NewLLM`, exactly as it would in
  production — both mock clients swap for live clients with no other change.
- **`ask_user` is a client tool** (`tool.Client`). Only a client-tool
  call sets `DoneClientToolCall`, which the driver keys on to park the task at
  `awaiting_input`. Registered server-side it would execute inline and never
  suspend.
- **The headless park is driven by the real `Dispatcher`** with a short poll
  interval and a `WithNotifier` callback. The only nondeterminism is timing,
  absorbed by waiting for the notification before stopping the dispatcher.

The first four turns run synchronously inside a single `StartTask` call; the
fifth runs on the dispatcher's goroutine.

## Testing

```bash
go test -race ./examples/task-lifecycle/...
```

`TestRunExample` asserts on the returned `Result` (not stdout): the draft task
is `done` with exactly one rejection, the proofread follow-on is `done`, the
detached session is parked at `awaiting_input`, and the notifier received the
parked task carrying the expected question. The mock LLMs return an error if the
scripted turn counts are wrong, so the test fails loudly if the lifecycle
deviates.

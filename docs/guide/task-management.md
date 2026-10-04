# Sessions & Task Management

Gantry models long-running work as three nested concepts. This page explains how
they relate and how the Task layer works. For the mechanics of the session
layer, see the [sessions guide](sessions.md).

> **Status.** All three layers — **Run**, **Session**, and **Task** — are
> implemented. The Task layer lives in two packages: `task` (the entity, ledger,
> store, and driver) and `taskmanager` (per-session orchestration, queues,
> spawning, scheduling, and notifications). Only in-memory stores ship today;
> see [What's still planned](#whats-still-planned).

## The three layers

| Layer | What it is | Lifetime | Package |
|-------|------------|----------|---------|
| **Run** | One bounded agent phase loop (`PhaseStart → … → PhaseEnd`), capped by `maxIterations` | One call | `gantry` |
| **Session** | Keyed, durable **chat context**: the user-facing transcript + conversational state | Many runs | `session` |
| **Task** | Keyed, durable **work item with a plan**: goal, plan-ledger, budget, status | Many runs | `task`, `taskmanager` |

```
Run        one bounded agent phase loop (PhaseStart → … → PhaseEnd), capped by maxIterations
Session    keyed chat context: user-facing transcript + conversational state
             (wraps each turn as Load → RunFrom → Save)
Task       keyed durable work item: goal + plan-ledger + budget + status
             (keyed to a session id, executed across many Runs by task.Driver)
```

The distinction that drives the design: a **Session is conversation**, a **Task
is goal-directed work**. Simple chat needs no task; a "do this bigger thing"
request spawns one.

## How Sessions work

A session is the **serialization boundary** and the durable container for a
conversation. Each turn is `Load(id) → RunFrom(prior, input) → Save(id)`, and the
full transcript is persisted and reloaded under the session id so state is shared
across every message. See the [sessions guide](sessions.md) for the detailed
flow and diagrams.

What a session carries across runs is deliberately narrow — `Messages`, `Usage`,
and `Meta`. The `session` package is a **pure chat layer**: it knows nothing
about tasks. Durable *work* state (a plan and its progress) lives in the Task
layer instead.

## How Tasks work

A **Task** (`task.Task`) is a separate, durable entity stored by id in a
`task.TaskStore`. It records the `SessionID` it belongs to, but the link back
from the session is **not** stored by the `session` package. It is a
`task.SessionMeta` — lightweight `TaskRef{ID, Title, Status, CreatedAt}`
entries, an `ActiveTaskID`, a FIFO `Queue` of waiting task ids, and `ChildRefs`
for detached child sessions — persisted by a `taskmanager.MetaStore` keyed by
session id. The session id is the only thing the two layers share.

A task keeps its own `Working` message context, separate from the chat
transcript, and owns a **plan-ledger** (`Task.Plan`) — the source of truth for
progress and completion. Each `gantry.PlanStep` carries a status
(`pending`/`active`/`done`/`failed`/`skipped`), optional `AcceptanceCriteria`,
and its `Output`. Before each run, `task.Hydrate` projects the ledger into the
run's `state.Plan` (bounding completed steps' output); after the run,
`task.Flush` reconciles step status and output back into the ledger and adopts
any steps the run appended.

### Lifecycle

```
pending → active → awaiting_input → done   (explicit, verifier-gated)
                 ↘ failed / cancelled
```

- A task becomes **`active`** only once its plan has at least one step.
- **`done`** is reached only when the driver's `Verifier` accepts a final answer
  — never auto-derived from "all steps done."
- **`pending`** doubles as the queued state.
- **`done`**, **`failed`**, and **`cancelled`** are terminal.

### Plans: skeleton + refine

On a task's first run the ledger is empty, so the `planner` component (if the
agent carries one) builds a coarse **skeleton**; `planner.NewLLM` can attach
per-step acceptance criteria. The model then **refines** the plan via the
`update_plan` pseudo-tool (`planner.UpdatePlan()`), setting step status,
recording output, or appending steps as it learns.

Completion authority is **hybrid**: the model self-reports step status during
execution, and a `task.Verifier` gates the final `done` transition. The default
is `task.NoopVerifier`, which accepts the first final answer; wire the `critic`
component in with `task.WithVerifier(task.NewCriticVerifier(c))`. A rejection
feeds the critique back to the model as a hidden user turn and starts another
run; after repeated rejections the task parks at `awaiting_input` for a human
reply. An optional `task.Replanner` (`planner.NewLLMReplanner`, wired with
`task.WithReplanner`) revises the ledger when rejections pile up or a step
newly fails.

### Orchestration & budgets

The **`task.Driver`** runs the loop across many runs: run → flush progress → if
the model gave a final answer and the verifier accepts it, finish; if `ask_user`
fired, suspend (`awaiting_input`) and resume on the next request
(`Advance` / `AdvanceWithAnswers`); otherwise, if budget remains, launch another
run.

Two budgets at two layers:

- **`maxIterations`** caps a single **run**.
- **`TaskBudget`** (`MaxRuns`, `MaxTokens`, `MaxCostUSD`) caps the **task**
  across runs. An exhausted budget fails the task.

When a run hits `maxIterations`, core gives the model one tool-less wrap-up
turn; the driver drops that answer from `Working` before continuing, so the
next run resumes from the pre-cap transcript.

`ask_user` never raises `maxIterations` — "needs input" is a clean suspension, not
a budget extension. It must be registered as a client tool (`tool.Client`) so the
call suspends the run instead of executing inline.

### Concurrency & the TaskManager

The **`taskmanager.TaskManager`** sits on top of the driver and enforces the
concurrency model:

- **Within a session:** one active task at a time. `StartTask`, `ResumeTask`,
  and `ResumeTaskWithAnswers` are serialized per session id; further tasks wait
  in the session's FIFO queue, gated by optional same-session `DependsOn` edges.
- **Across sessions:** tasks run in **parallel** — parallelism is "N sessions × 1
  active task each."
- **Spawning:** a running task can add same-session follow-on work with the
  `create_task` tool, or start **unrelated** work in a new, detached session
  with the `spawn_session` tool. A `SpawnPolicy` caps spawn depth and derives
  child budgets.
- **Headless sessions:** a `Dispatcher` drains a claim-based `ReadyQueue` and
  drives sessions with no live chat; a `Scheduler` starts detached sessions at
  a set time. A headless task that needs a human parks at `awaiting_input` and
  fires the dispatcher's notifier (`NotifyBridge` records it in a
  `NotificationStore`).
- **Recovery and cancellation:** `Recover` re-enqueues drivable sessions after
  a crash, and `CancelSession` cancels a session's work and its spawned
  children.

The model can inspect work in progress with the `list_tasks` and `task_status`
tools.

For runnable walkthroughs, see
[`examples/task-lifecycle`](https://github.com/farazhassan/gantry/tree/main/examples/task-lifecycle)
(deterministic, scripted LLMs) and
[`examples/task-lifecycle-live`](https://github.com/farazhassan/gantry/tree/main/examples/task-lifecycle-live)
(the same wiring against a live Ollama model).

## What's still planned

- **Durable backends.** `task.TaskStore`, `taskmanager.MetaStore`,
  `ReadyQueue`, `ScheduleStore`, and `NotificationStore` ship with in-memory
  implementations only, so task state does not survive a process restart yet.
  `conformance.TaskStoreSuite` defines the contract a durable `TaskStore` must
  meet.
- **Handoff inside tasks.** A run that requests a handoff fails its task with a
  named cause; handoff is supported only at the session layer today.
- **Cross-session dependencies.** `DependsOn` edges must stay within one
  session.

## Why this design

The durable-state problem isn't solved by carrying more `State` fields across
runs. It's solved by giving work-state a **proper, typed home — the task
ledger** — owned by a first-class `Task` entity, instead of smuggling it through
the untyped, serialization-fragile `Meta` map.

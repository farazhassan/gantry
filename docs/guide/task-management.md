# Sessions & Task Management

Gantry models long-running work as three nested concepts. This page explains how
they relate. For the mechanics of the *existing* session layer, see the
[sessions guide](sessions.md).

> **Status.** The **Run** and **Session** layers are implemented today. The
> **Task** layer described below is a **design under review** and is not yet
> implemented. Sections covering Tasks describe planned behavior, not current
> code.

## The three layers

| Layer | What it is | Lifetime | Status |
|-------|------------|----------|--------|
| **Run** | One bounded agent phase loop (`PhaseStart → … → PhaseEnd`), capped by `maxIterations` | One call | Implemented |
| **Session** | Keyed, durable **chat context**: the user-facing transcript + conversational state | Many runs | Implemented |
| **Task** | Keyed, durable **work item with a plan**: goal, plan-ledger, budget, status | Many runs | Planned |

```
Run        one bounded agent phase loop (PhaseStart → … → PhaseEnd), capped by maxIterations
Session    keyed chat context: user-facing transcript + conversational state
             (wraps each turn as Load → RunFrom → Save)
Task       keyed durable work item: goal + plan-ledger + budget + status
             (owned by a Session, executed across many Runs by a task driver)
```

The distinction that drives the design: a **Session is conversation**, a **Task
is goal-directed work**. Simple chat needs no task; a "do this bigger thing"
request spawns one.

## How Sessions work today

A session is the **serialization boundary** and the durable container for a
conversation. Each turn is `Load(id) → RunFrom(prior, input) → Save(id)`, and the
full transcript is persisted and reloaded under the session id so state is shared
across every message. See the [sessions guide](sessions.md) for the detailed
flow and diagrams.

What carries across runs today is deliberately narrow — `Messages`, `Usage`, and
`Meta`. Durable *work* state (a plan and its progress) has no home yet; that is
exactly what the Task layer adds.

## How Tasks will work (planned)

A **Task** is a separate, durable entity owned by a session. The session keeps
only lightweight references (`TaskRef{ID, Title, Status, CreatedAt}` +
`ActiveTaskID`); the heavy task state lives in a `TaskStore` keyed by task id.

A task owns a **plan-ledger** — the source of truth for progress and completion.
Each step carries a status (`pending`/`active`/`done`/`failed`/`skipped`),
acceptance criteria, and its output. The per-run `state.Plan` becomes a *hydrated
projection* of this ledger rather than a throwaway rebuilt each run.

### Lifecycle

```
pending → active → awaiting_input → done   (explicit, verifier-gated)
                 ↘ failed / cancelled
```

- A task may only become **`active`** with a non-empty plan (≥1 step).
- **`done`** is reached only by an explicit transition gated by a verifier — never
  auto-derived from "all steps done."
- **`pending`** doubles as the queued/scheduled state.

### Plans: skeleton + refine

The planner produces a coarse **skeleton** on a task's first run (with per-step
acceptance criteria), then the model **refines** it via an `update_plan` tool as
it learns. Completion authority is **hybrid**: the model self-reports step status
during execution; the `critic` component acts as the verifier that gates the
final `done` transition.

### Orchestration & budgets

A **task driver** runs the loop across many runs: run → flush progress → if the
plan is complete and verified, finish; if `ask_user` fired, suspend
(`awaiting_input`) and resume on the next request; otherwise, if budget remains,
launch another run.

Two budgets at two layers:

- **`maxIterations`** caps a single **run**.
- **`TaskBudget`** caps the **task** across runs.

`ask_user` never raises `maxIterations` — "needs input" is a clean suspension, not
a budget extension.

### Concurrency

- **Within a session:** one active task at a time.
- **Across sessions:** tasks run in **parallel** — parallelism is "N sessions × 1
  active task each."
- **New / unrelated / scheduled** work spawns a **new session** (no shared
  context); related follow-on work stays in the current session and queues behind
  the active task.
- **Headless sessions** are allowed: a scheduled task with no live chat is still a
  session; it parks `awaiting_input` and notifies when it needs a human.

## Why this design

The durable-state problem isn't solved by carrying more `State` fields across
runs. It's solved by giving work-state a **proper, typed home — the task
ledger** — owned by a first-class `Task` entity, instead of smuggling it through
the untyped, serialization-fragile `Meta` map.

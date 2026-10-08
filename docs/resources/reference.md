# Gantry Reference

Detailed reference for Gantry's components, conformance suites, eval harness,
testing, and repository layout. For the overview, install steps, quick start,
and core concepts, see the [main README](https://github.com/farazhassan/gantry#readme). For keyed, durable
multi-turn conversations, see the [sessions guide](../guide/sessions.md).

## Contents

- [Components](#components)
- [Conformance](#conformance)
- [Eval](#eval)
- [Testing](#testing)
- [Project layout](#project-layout)

## Components

Components don't know about phases — each package's `New` constructor returns a
`gantry.Component` that translates itself into the right middleware on the right
phase. Install them at construction with `gantry.WithComponents(...)` or
afterward with `a.With(...)`; both return any wiring error. Mix and match what
you need.

| Component | What it does | Wire it up | Built-ins |
|-----------|--------------|------------|-----------|
| **transcript** | Persists & reads conversation history across runs | `transcript.New(t)` | `NewInMemoryStore()` |
| **memory** | Vector-backed long-term memory: recalls top-k similar past turns into context, persists the final turn pair | `memory.New(store, emb)` | `vectorstore.NewInMemoryStore()` |
| **sqlitevec** | `vectorstore.Store` on SQLite + sqlite-vec (pure Go, own module) | `sqlitevec.New(path, sqlitevec.WithDim(d))` | — |
| **tool** | Capabilities the LLM can invoke, with parallel dispatch and a configurable failure policy | `tool.FromTools(parallelism, tools...)` · `tool.New(reg, parallelism)` · `tool.FromToolsWithPolicy(policy, tools...)` · `tool.NewWithPolicy(reg, policy)` · `tool.Client(defs...)` | `NewRegistry()` |
| **skill** | Conditional instruction/context blocks injected into the system prompt | `skill.New(s)` | `NewStatic(name, prompt)` |
| **retriever** | Fetches top-`k` docs for RAG and injects them | `retriever.New(r, k)` | `NewStatic(docs)` · `NewVectorRetriever(store, emb)` |
| **planner** | Decomposes the task into a plan up front | `planner.New(p)` | `NewLLM(client, rubric)` |
| **critic** | Self-reviews the last response (pass / reject) | `critic.New(c)` | `NewLLM(client, rubric)` |
| **guardrail** | Validates inputs (pre-LLM) and outputs (post-LLM) | `guardrail.New(g)` | `NewRegex(pattern, direction)` |
| **limiter** | Caps tokens, cost, and iterations; stops the run when exceeded | `limiter.New(l)` | `NewBudget(Limits{...})` |
| **compactor** | Trims history before the LLM call; on a context-overflow error compacts once more (forced) and retries. `EstimatePromptTokens(state, budget)` gives the provider-measured prompt size plus an estimate for newer messages | `compactor.New(c, budget)` | `NewPolicy(p)` with steps `ClearToolResults(keepResults)` · `TruncateMessages(maxTokens)` · `SummarizeTurns(client, keepTurns)` · `DropTurns(keepTurns, pinFirst)`; `NewSlidingWindow(n)` · `NewHeadTail(head, tail)` · `NewSummarizing(client, head, tail)` |
| **humanloop** | Pauses for human approval before tool execution | `humanloop.New(h)` | `NewAutoApprover()` · `NewAutoDenier(reason)` |
| **checkpointer** | Saves & restores state by id for resume / replay; optionally saves mid-run too (see `extraPhases`) | `checkpointer.New(c, id, extraPhases...)` | — |
| **checkpointer/mem** | `checkpointer.Checkpointer` and `checkpointer.Lease` backed by in-memory stores (tests, examples) | `mem.New()` · `mem.NewLease()` | `NewStore()` |
| **checkpointer/file** | `checkpointer.Checkpointer` backed by a file store (one file per id, atomic writes) | `file.New(dir)` | `NewStore(dir)` |
| **checkpointer/sql** | `checkpointer.Store` on any `database/sql` connection (SQLite, Postgres, …) | `sql.New(db)` via `checkpointer.FromStore` | — |
| **checkpointer/redis** | `checkpointer.Store` and `checkpointer.Lease` on Redis (own module) | `redis.New(rdb)` via `checkpointer.FromStore` · `redis.NewLease(rdb)` | — |
| **checkpointer/etcd** | `checkpointer.Lease` on etcd (own module) | `etcd.New(cli)` | — |

Each built-in is a reference implementation — swap in your own (a vector-store
retriever, a real guardrail service) by satisfying the component's interface.
`checkpointer/mem`, `checkpointer/file`, and `checkpointer/sql` need no
third-party dependency and live in the root module; `checkpointer/redis`,
`checkpointer/etcd`, and `sqlitevec` do need one, so each lives in its own Go
module.

For crash recovery across horizontally-scaled workers, pair a `checkpointer.Lease`
(`redis.NewLease`/`etcd.New`) with `checkpointer.ResumeLocked`, which wraps
Acquire → Load → Resume → Release into one call — see
`examples/checkpoint-resume`.

`memory` and `retriever`'s `NewVectorRetriever` are two policies — read-write
and read-only — over one shared **`components/vectorstore`** `Store` interface
(`Add`/`Search` over embedded items). `vectorstore.NewInMemoryStore()` is the
built-in backend; `sqlitevec` is a durable one. Verify a backend with
`conformance.VectorStoreSuite`.

### Context window

- `gantry.WithContextWindow(n)` — the model's maximum prompt tokens. When set, the client's lookup is skipped entirely; otherwise the client's lookup is preferred over a value carried from a previous turn (the carried value is only a fallback). Copied to `State.ContextWindow` each run (0 = unknown). The Anthropic and OpenRouter adapters look the window up automatically; OpenAI needs this option; Ollama reports `ollama.WithNumCtx(n)`.
- `State.ContextUsage` anchors the last provider-measured prompt size (`Usage.InputTokens`, which includes cached tokens on every adapter) to the transcript.
- `gantry.ErrContextLengthExceeded` — the provider rejected the prompt as longer than the model's context window. Adapters return a `*gantry.ContextLengthError` (with `Limit`/`Requested` when the provider reports them) that matches it via `errors.Is`. The compactor component recovers by compacting and retrying once.
- `a.OnContextOverflow(h)` — registers the agent's single `ContextOverflowHandler`, which the compactor component installs. On an overflow error it may shrink the state and ask for one retry; the core loop then re-runs the whole `PhaseLLMCall` middleware chain, so guardrails and the limiter check the shrunk input. A `StopReasonContextWindow` response that contains tool calls (the window filled mid tool call) is treated as an overflow, so the possibly cut-off calls never run.

### Compaction policies

`compactor.NewPolicy` compacts only when it is needed, using the provider's numbers:

```go
a.With(compactor.New(compactor.NewPolicy(compactor.Policy{
	// Trigger: 0.8, Target: 0.5 are the defaults (fractions of State.ContextWindow).
	Steps: []compactor.Compactor{
		compactor.ClearToolResults(5),     // old tool output beyond the newest 5 results → placeholder
		compactor.TruncateMessages(8000),  // cap any single huge message
		compactor.SummarizeTurns(llm, 4),  // oldest turns → one rolling summary
		compactor.DropTurns(2, true),      // last resort; keeps the first turn
	},
}), compactor.Budget{}))
```

- **Trigger and target.** Nothing changes until the prompt (`State.ContextUsage` plus an estimate of newer messages) reaches `Trigger × ContextWindow`; then steps run in order until the messages fit `Target × ContextWindow` minus System and Tools. The gap means compaction runs rarely, so the provider's prompt cache keeps its prefix in between. Without a known window, `TriggerTokens`/`TargetTokens` apply; with neither, the policy compacts only when the provider rejects a prompt (`ErrContextLengthExceeded`), toward the reported limit.
- **Calibration.** The bytes/4 estimate is scaled by the ratio of measured to estimated prompt tokens (clamped to 0.5–2×), so steps cut in provider tokens.
- **Turn-aware steps.** A turn is a user message plus the assistant and tool messages after it. Steps remove whole turns or rewrite message content, so a tool call is never separated from its result; any `Compactor` can be a step, and tool results orphaned by one are dropped, as are tool calls it left without a result.
- **Report.** `State.Meta[compactor.MetaLastCompaction]` holds a `*compactor.Report` (before/after/target tokens, forced, each step that ran, and the `Usage` its summarizer calls spent) for the latest compaction.
- **Usage accounting.** Tokens spent by summarizer calls (`SummarizeTurns`, `Summarizing`) are added to `State.Usage`, and the limiter records them at its next check, so they count toward its `MaxTokens` cap.

### Putting it together

`examples/e2e` wires the core components onto a single agent and runs a scripted
scenario end to end. It does RAG through the `memory` component (a small
knowledge base embedded into `vectorstore.NewInMemoryStore()` with a deterministic
stand-in embedder, so it runs under `go test` with no API keys); the `sqlitevec`
and `qdrant` backends are covered separately:

```go
a, _ := gantry.NewAgent(
	gantry.WithLLM(scriptedLLM),
	gantry.WithMaxIterations(8),
	gantry.WithComponents(
		transcript.New(transcript.NewInMemoryStore()),
		skill.New(skill.NewStatic("careful", "Be careful with numbers and cite the tool you used.")),
		memory.New(knowledgeBase, embedder, memory.WithK(1)),
		compactor.New(compactor.NewSlidingWindow(20), compactor.Budget{}),
		tool.FromTools(4, calcTool{}),
		limiter.New(limiter.NewBudget(limiter.Limits{MaxTokens: 10_000, MaxCostUSD: 1.0})),
		guardrail.New(guardrail.NewRegex(`(?i)forbidden`, guardrail.DirectionOutput)),
		critic.New(critic.NewLLM(helperLLM, "Reply PASS if the answer is correct; FAIL otherwise.")),
		planner.New(planner.NewLLM(helperLLM, "Break the task into numbered steps.")),
		humanloop.New(humanloop.NewAutoApprover()),
		checkpointer.New(mem.New(), "example-run"),
	),
)

state, _ := a.Run(ctx, "what is 2 + 3?")
```

Run it:

```sh
go run ./examples/e2e
```

## Conformance

Writing your own component implementation? The `conformance` package ships
reusable test suites that verify an implementation honors its contract. Drop one
into a `_test.go` and pass a factory:

```go
func TestMyTranscript(t *testing.T) {
	conformance.TranscriptSuite(t, func() transcript.Transcript {
		return mypkg.NewTranscript()
	})
}
```

Suites are provided for every contract: `Transcript`, `VectorStore`, `Tool`,
`Checkpointer`, `Lease`, `Compactor`, `Critic`, `Guardrail`, `HumanInLoop`,
`Limiter`, `Planner`, `Retriever`, `LLMClient`, and `Tracer`.

## Eval

The `eval` package treats agents as black boxes (via an `AgentFactory`) and sweeps
configurations × cases × scorers into an aggregated report.

- **Datasets:** `JSONLDataset` (load from a `.jsonl` file) or `SliceDataset` (in-memory).
- **Scorers:** `ExactMatch`, `Regex`, `Contains`, `Trace`, `Usage`, `Latency`, and
  `LLMJudge` — or implement the `Scorer` interface yourself.
- **Runner:** coordinates the sweep and returns a `Report` with per-case scores and aggregates.
- **MockLLMClient:** `NewMockLLMClient(responses...)` scripts deterministic LLM
  replies so evals (and tests) are reproducible.

## Testing

```sh
go test ./...           # root module (nested modules with their own go.mod are tested separately)
go test -race ./...     # root module with the race detector (what CI runs)
go vet ./...
gofmt -l .              # lists files needing formatting (empty = clean)
```

### Continuous integration

Every push and pull request runs the [CI workflow](https://github.com/farazhassan/gantry/blob/main/.github/workflows/ci.yml), split
into jobs that double as required status checks for branch protection on `main`:

- **Lint & format** — `gofmt` check, `go vet`, and `staticcheck`.
- **Build** — `go build ./...` on Linux, macOS, and Windows.
- **Test** — `go test -race` with coverage on Go 1.24 and the latest stable Go.
- **Tidy** — `go mod verify` plus a `go mod tidy` no-op check.

Two more workflows complete the pipeline:

- **[Release](https://github.com/farazhassan/gantry/blob/main/.github/workflows/release.yml)** — triggered by a pushed `v*` tag;
  re-runs the build and tests, then publishes a GitHub Release with auto-generated
  notes (tags with a pre-release suffix like `v0.0.1-beta` are flagged as
  pre-releases).
- **[CodeQL](https://github.com/farazhassan/gantry/blob/main/.github/workflows/codeql.yml)** — security and quality scanning on
  pushes, PRs, and a weekly schedule.

[Dependabot](https://github.com/farazhassan/gantry/blob/main/.github/dependabot.yml) keeps Go modules and GitHub Actions versions
up to date.

## Project layout

```
./            Core agent loop (package gantry): phases, middleware, State, and the LLMClient interface
components/   Drop-in capabilities (transcript, memory, tool, skill, retriever, planner, critic,
              guardrail, limiter, compactor, humanloop, checkpointer)
conformance/  Reusable test suites that verify implementations satisfy each contract
eval/         Dataset / scorer / runner harness plus a scriptable mock LLM client
examples/     Runnable end-to-end example wiring every component together
docs/         Reference documentation (this file) plus design specs and plans
```

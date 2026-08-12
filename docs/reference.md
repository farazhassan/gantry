# Gantry Reference

Detailed reference for Gantry's components, conformance suites, eval harness,
testing, and repository layout. For the overview, install steps, quick start,
and core concepts, see the [main README](../README.md). For keyed, durable
multi-turn conversations, see the [sessions guide](sessions.md).

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
| **compactor** | Trims history to fit a token budget before the LLM call | `compactor.New(c, budget)` | `NewSlidingWindow(n)` · `NewHeadTail(head, tail)` · `NewSummarizing(client, head, tail)` |
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
Load → Acquire → Resume → Release into one call — see
`examples/checkpoint-resume`.

`memory` and `retriever`'s `NewVectorRetriever` are two policies — read-write
and read-only — over one shared **`components/vectorstore`** `Store` interface
(`Add`/`Search` over embedded items). `vectorstore.NewInMemoryStore()` is the
built-in backend; `sqlitevec` is a durable one. Verify a backend with
`conformance.VectorStoreSuite`.

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
`Checkpointer`, `Compactor`, `Critic`, `Guardrail`, `HumanInLoop`, `Limiter`,
`Planner`, `Retriever`, `LLMClient`, and `Tracer`.

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

Every push and pull request runs the [CI workflow](../.github/workflows/ci.yml), split
into jobs that double as required status checks for branch protection on `main`:

- **Lint & format** — `gofmt` check, `go vet`, and `staticcheck`.
- **Build** — `go build ./...` on Linux, macOS, and Windows.
- **Test** — `go test -race` with coverage on Go 1.22 and the latest stable Go.
- **Tidy** — `go mod verify` plus a `go mod tidy` no-op check.

Two more workflows complete the pipeline:

- **[Release](../.github/workflows/release.yml)** — triggered by a pushed `v*` tag;
  re-runs the build and tests, then publishes a GitHub Release with auto-generated
  notes (tags with a pre-release suffix like `v0.0.1-beta` are flagged as
  pre-releases).
- **[CodeQL](../.github/workflows/codeql.yml)** — security and quality scanning on
  pushes, PRs, and a weekly schedule.

[Dependabot](../.github/dependabot.yml) keeps Go modules and GitHub Actions versions
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

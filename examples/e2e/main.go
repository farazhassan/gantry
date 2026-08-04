package main

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"math"
	"os"
	"strings"
	"unicode"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/components/checkpointer/mem"
	"github.com/farazhassan/gantry/components/compactor"
	"github.com/farazhassan/gantry/components/critic"
	"github.com/farazhassan/gantry/components/embeddings"
	"github.com/farazhassan/gantry/components/guardrail"
	"github.com/farazhassan/gantry/components/humanloop"
	"github.com/farazhassan/gantry/components/limiter"
	"github.com/farazhassan/gantry/components/memory"
	"github.com/farazhassan/gantry/components/planner"
	"github.com/farazhassan/gantry/components/skill"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/components/transcript"
	"github.com/farazhassan/gantry/components/vectorstore"
	"github.com/farazhassan/gantry/eval"
)

// calcTool is a trivial tool used by the example.
type calcTool struct{}

func (calcTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{
		Name:        "calc",
		Description: "adds two integers",
		Schema:      json.RawMessage(`{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"integer"}}}`),
	}
}

func (calcTool) Invoke(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	var args struct{ A, B int }
	if err := json.Unmarshal(in, &args); err != nil {
		return nil, err
	}
	return json.Marshal(args.A + args.B)
}

// docEmbedder is a tiny, deterministic stand-in embedder for the example. It
// hashes each lowercased alphanumeric token into a fixed-width bag-of-words
// vector and L2-normalizes it — no ML, no network. It exists only to show the
// memory component's mechanism end to end (embed a query, rank stored items by
// cosine similarity, inject the nearest into context) under `go test` with no
// API keys. Swap in embeddings/openai or embeddings/voyage for real use.
type docEmbedder struct{}

const embedDim = 256

// compile-time check that the stand-in satisfies the interface real adapters do.
var _ embeddings.Embeddings = docEmbedder{}

func (docEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, embedDim)
		for _, tok := range strings.FieldsFunc(strings.ToLower(t), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r)
		}) {
			h := fnv.New32a()
			_, _ = h.Write([]byte(tok))
			v[h.Sum32()%embedDim]++
		}
		var norm float64
		for _, x := range v {
			norm += float64(x) * float64(x)
		}
		if norm > 0 {
			norm = math.Sqrt(norm)
			for j := range v {
				v[j] = float32(float64(v[j]) / norm)
			}
		}
		out[i] = v
	}
	return out, nil
}

// newKnowledgeBase seeds an in-memory vector store with a few facts, embedded
// with emb. The memory component then serves these as retrieval-augmented
// context: recall injects the fact nearest to each run's query.
func newKnowledgeBase(emb embeddings.Embeddings) (*vectorstore.InMemoryStore, error) {
	facts := []string{
		"Arithmetic like 2 + 3 is handled by the calc tool.",
		"The capital of France is Paris.",
		"Water boils at 100 degrees Celsius at sea level.",
	}
	vecs, err := emb.Embed(context.Background(), facts)
	if err != nil {
		return nil, err
	}
	items := make([]vectorstore.Item, len(facts))
	for i, f := range facts {
		items[i] = vectorstore.Item{Text: f, Vector: vecs[i], Metadata: map[string]any{"source": "kb"}}
	}
	store := vectorstore.NewInMemoryStore()
	if err := store.Add(context.Background(), items...); err != nil {
		return nil, err
	}
	return store, nil
}

// BuildAgent constructs an Agent with every first-class component attached.
// scriptedLLM is the user-facing LLM; helperLLM is used by Planner and Critic
// (in a real system these could be the same or different models).
func BuildAgent(scriptedLLM, helperLLM gantry.LLMClient) (*gantry.Agent, *checkpointer.StoreCheckpointer, *limiter.BudgetLimiter, error) {
	a, err := gantry.NewAgent(
		gantry.WithLLM(scriptedLLM),
		gantry.WithMaxIterations(8),
	)
	if err != nil {
		return nil, nil, nil, err
	}

	// Transcript
	if err := a.With(transcript.New(transcript.NewInMemoryStore())); err != nil {
		return nil, nil, nil, err
	}

	// Skill
	if err := a.With(skill.New(skill.NewStatic("careful", "Be careful with numbers and cite the tool you used."))); err != nil {
		return nil, nil, nil, err
	}

	// Memory (vector RAG): a small knowledge base is embedded into an
	// in-memory vector store; on each run the recall middleware embeds the
	// query, finds the nearest fact by cosine similarity, and injects it into
	// the system prompt. (persist also stores each finished turn, so the store
	// doubles as long-term conversational memory across runs.)
	emb := docEmbedder{}
	kb, err := newKnowledgeBase(emb)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := a.With(memory.New(kb, emb, memory.WithK(1))); err != nil {
		return nil, nil, nil, err
	}

	// Compactor — sliding window of 20 messages.
	if err := a.With(compactor.New(compactor.NewSlidingWindow(20), compactor.Budget{})); err != nil {
		return nil, nil, nil, err
	}

	// Tool with parallel dispatch (capacity 4).
	if err := a.With(tool.FromTools(4, calcTool{})); err != nil {
		return nil, nil, nil, err
	}

	// Limiter — token + cost ceiling.
	lim := limiter.NewBudget(limiter.Limits{MaxTokens: 10_000, MaxCostUSD: 1.0})
	if err := a.With(limiter.New(lim)); err != nil {
		return nil, nil, nil, err
	}

	// Guardrail — block any output that contains "forbidden".
	if err := a.With(guardrail.New(guardrail.NewRegex(`(?i)forbidden`, guardrail.DirectionOutput))); err != nil {
		return nil, nil, nil, err
	}

	// Critic — review with helperLLM.
	if err := a.With(critic.New(critic.NewLLM(helperLLM, "Reply PASS if the answer is correct; FAIL otherwise."))); err != nil {
		return nil, nil, nil, err
	}

	// Planner — produce a plan up front using helperLLM.
	if err := a.With(planner.New(planner.NewLLM(helperLLM, "Break the task into numbered steps."))); err != nil {
		return nil, nil, nil, err
	}

	// HumanInLoop — auto-approve in the example; CLI/web adapters could deny.
	if err := a.With(humanloop.New(humanloop.NewAutoApprover())); err != nil {
		return nil, nil, nil, err
	}

	// Checkpointer
	cp := mem.New()
	if err := a.With(checkpointer.New(cp, "example-run")); err != nil {
		return nil, nil, nil, err
	}

	return a, cp, lim, nil
}

// RunExample executes a single scripted scenario and prints the trace.
func RunExample(ctx context.Context) error {
	// Scripted main LLM: turn 1 calls the calc tool, turn 2 gives the final answer.
	scriptedLLM := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "calc", Input: json.RawMessage(`{"A":2,"B":3}`)}},
			StopReason: gantry.StopReasonToolUse,
			Usage:      gantry.Usage{InputTokens: 100, OutputTokens: 30, Cost: 0.001},
		},
		gantry.LLMResponse{
			Content:    "The answer is 5 (computed by the calc tool).",
			StopReason: gantry.StopReasonEnd,
			Usage:      gantry.Usage{InputTokens: 50, OutputTokens: 15, Cost: 0.0005},
		},
	)

	// Helper LLM: planner returns a 3-step plan (turn 1) as the propose_plan
	// tool call its forced ToolChoice requires; the critic runs in
	// PhasePostLLM on every iteration, so it is invoked once per main turn.
	// With two main turns (tool-call, then final answer) that is two critic
	// calls, so the critic needs two PASS verdicts (turns 2 and 3).
	helperLLM := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{{ID: "p1", Name: "propose_plan", Input: json.RawMessage(
				`{"steps":[{"description":"parse inputs"},{"description":"invoke calc"},{"description":"report"}]}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "VERDICT: PASS — proceeding with the tool call."},
		gantry.LLMResponse{Content: "VERDICT: PASS — answer matches the tool output."},
	)

	agent, cp, lim, err := BuildAgent(scriptedLLM, helperLLM)
	if err != nil {
		return err
	}

	state, err := agent.Run(ctx, "what is 2 + 3?")
	if err != nil {
		return fmt.Errorf("agent run: %w", err)
	}

	fmt.Println("=== Final output ===")
	fmt.Println(state.FinalOutput)
	fmt.Println()
	fmt.Println("=== Done reason ===")
	fmt.Println(state.DoneReason)
	fmt.Println()
	fmt.Println("=== Usage ===")
	fmt.Printf("tokens=%d  cost=$%.4f  iterations=%d\n", state.Usage.InputTokens+state.Usage.OutputTokens, state.Usage.Cost, state.Iteration)
	fmt.Println("=== Limiter total ===")
	t := lim.Total()
	fmt.Printf("tokens=%d  cost=$%.4f\n", t.InputTokens+t.OutputTokens, t.Cost)

	// Checkpoint round-trip demo.
	loaded, err := cp.Load(ctx, "example-run")
	if err != nil {
		return fmt.Errorf("checkpoint load: %w", err)
	}
	fmt.Println()
	fmt.Println("=== Checkpoint loaded ===")
	fmt.Printf("input=%q  iterations=%d  final=%q\n", loaded.Input, loaded.Iteration, loaded.FinalOutput)

	fmt.Println()
	fmt.Println("=== Trace events (count) ===")
	fmt.Println(len(state.Trace.Snapshot()))

	return nil
}

func main() {
	if err := RunExample(context.Background()); err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
}

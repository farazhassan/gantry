package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/components/compactor"
	"github.com/farazhassan/gantry/components/critic"
	"github.com/farazhassan/gantry/components/guardrail"
	"github.com/farazhassan/gantry/components/humanloop"
	"github.com/farazhassan/gantry/components/limiter"
	"github.com/farazhassan/gantry/components/memory"
	"github.com/farazhassan/gantry/components/planner"
	"github.com/farazhassan/gantry/components/retriever"
	"github.com/farazhassan/gantry/components/skill"
	"github.com/farazhassan/gantry/components/tool"
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

// BuildAgent constructs an Agent with every first-class component attached.
// scriptedLLM is the user-facing LLM; helperLLM is used by Planner and Critic
// (in a real system these could be the same or different models).
func BuildAgent(scriptedLLM, helperLLM gantry.LLMClient) (*gantry.Agent, *checkpointer.InMemoryCheckpointer, *limiter.BudgetLimiter, error) {
	a, err := gantry.NewAgent(
		gantry.WithLLM(scriptedLLM),
		gantry.WithMaxIterations(8),
	)
	if err != nil {
		return nil, nil, nil, err
	}

	// Memory
	if err := a.With(memory.New(memory.NewInMemoryStore())); err != nil {
		return nil, nil, nil, err
	}

	// Skill
	skill.WithSkill(a, skill.NewStatic("careful", "Be careful with numbers and cite the tool you used."))

	// Retriever (RAG)
	retriever.WithRetriever(a, retriever.NewStatic([]gantry.Document{
		{ID: "doc-arith", Content: "Arithmetic is performed by the calc tool."},
	}), 3)

	// Compactor — sliding window of 20 messages.
	compactor.WithCompactor(a, compactor.NewSlidingWindow(20), compactor.Budget{})

	// Tool with parallel dispatch (capacity 4).
	tool.WithTools(a, 4, calcTool{})

	// Limiter — token + cost ceiling.
	lim := limiter.NewBudget(limiter.Limits{MaxTokens: 10_000, MaxCostUSD: 1.0})
	if err := a.With(limiter.New(lim)); err != nil {
		return nil, nil, nil, err
	}

	// Guardrail — block any output that contains "forbidden".
	guardrail.WithGuardrail(a, guardrail.NewRegex(`(?i)forbidden`, guardrail.DirectionOutput))

	// Critic — review with helperLLM.
	if err := a.With(critic.New(critic.NewLLM(helperLLM, "Reply PASS if the answer is correct; FAIL otherwise."))); err != nil {
		return nil, nil, nil, err
	}

	// Planner — produce a plan up front using helperLLM.
	if err := planner.WithPlanner(a, planner.NewLLM(helperLLM, "Break the task into numbered steps.")); err != nil {
		return nil, nil, nil, err
	}

	// HumanInLoop — auto-approve in the example; CLI/web adapters could deny.
	humanloop.WithHumanInLoop(a, humanloop.NewAutoApprover())

	// Checkpointer
	cp := checkpointer.NewInMemory()
	checkpointer.WithCheckpointer(a, cp, "example-run")

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

	// Helper LLM: planner returns a 3-step plan (turn 1); the critic runs in
	// PhasePostLLM on every iteration, so it is invoked once per main turn.
	// With two main turns (tool-call, then final answer) that is two critic
	// calls, so the critic needs two PASS verdicts (turns 2 and 3).
	helperLLM := eval.NewMockLLMClient(
		gantry.LLMResponse{Content: "1. parse inputs\n2. invoke calc\n3. report"},
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

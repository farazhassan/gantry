package harness_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

// respWith builds a no-tool-call response carrying the given content and usage.
func respWith(content string, in, out int) harness.LLMResponse {
	return harness.LLMResponse{
		Content:    content,
		StopReason: harness.StopReasonEnd,
		Usage:      harness.Usage{InputTokens: in, OutputTokens: out},
	}
}

func TestRunFromCarriesTranscriptAndResetsScratch(t *testing.T) {
	llm := eval.NewMockLLMClient(respWith("new answer", 3, 2))
	a, err := harness.New(harness.WithLLM(llm))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	prior := harness.NewState("old input")
	prior.Messages = []harness.Message{
		{Role: harness.RoleUser, Content: "first question"},
		{Role: harness.RoleAssistant, Content: "first answer"},
	}
	prior.Usage = harness.Usage{InputTokens: 5, OutputTokens: 5}
	prior.Meta = map[string]any{"k": "v"}
	// Stale scratch that MUST be reset on the new turn.
	prior.Iteration = 99
	prior.Done = true
	prior.DoneReason = harness.DoneNoToolCalls
	prior.FinalOutput = "stale final"

	got, err := a.RunFrom(context.Background(), prior, "second question")
	if err != nil {
		t.Fatalf("RunFrom: %v", err)
	}

	// Transcript: [prior two, new user, new assistant].
	if len(got.Messages) != 4 {
		t.Fatalf("len(Messages) = %d, want 4 (2 prior + new user + new assistant)", len(got.Messages))
	}
	if got.Messages[0].Content != "first question" || got.Messages[2].Content != "second question" {
		t.Errorf("transcript = %+v, want prior then new user input", got.Messages)
	}
	if got.Messages[2].Role != harness.RoleUser {
		t.Errorf("Messages[2].Role = %q, want user", got.Messages[2].Role)
	}

	// Scratch reset: ran exactly one iteration from 0 and terminated freshly.
	if got.Iteration != 1 {
		t.Errorf("Iteration = %d, want 1 (reset to 0 then one loop)", got.Iteration)
	}
	if got.DoneReason != harness.DoneNoToolCalls {
		t.Errorf("DoneReason = %q, want %q", got.DoneReason, harness.DoneNoToolCalls)
	}
	if got.FinalOutput != "new answer" {
		t.Errorf("FinalOutput = %q, want %q", got.FinalOutput, "new answer")
	}

	// Usage carried + accumulated: prior 5/5 + response 3/2.
	if got.Usage.InputTokens != 8 || got.Usage.OutputTokens != 7 {
		t.Errorf("Usage = %+v, want {8 7 0}", got.Usage)
	}

	// Meta carried.
	if got.Meta["k"] != "v" {
		t.Errorf("Meta[k] = %v, want v", got.Meta["k"])
	}

	// Fresh trace (different pointer than prior's).
	if got.Trace == prior.Trace {
		t.Error("Trace was carried; want a fresh Trace per turn")
	}
}

func TestRunFromMessagesAreIndependentOfPrior(t *testing.T) {
	llm := eval.NewMockLLMClient(respWith("answer", 0, 0))
	a, _ := harness.New(harness.WithLLM(llm))

	prior := harness.NewState("x")
	prior.Messages = []harness.Message{{Role: harness.RoleUser, Content: "original"}}
	prior.Meta = map[string]any{"k": "v"}

	got, err := a.RunFrom(context.Background(), prior, "next")
	if err != nil {
		t.Fatalf("RunFrom: %v", err)
	}

	// Mutating prior after the call must not affect the returned state.
	prior.Messages[0].Content = "MUTATED"
	prior.Meta["k"] = "MUTATED"

	if got.Messages[0].Content != "original" {
		t.Error("Messages alias prior's backing array; want an independent copy")
	}
	if got.Meta["k"] != "v" {
		t.Error("Meta aliases prior's map; want an independent copy")
	}
}

func TestRunFromNilPriorEqualsRun(t *testing.T) {
	llmA := eval.NewMockLLMClient(respWith("answer", 1, 1))
	a, _ := harness.New(harness.WithLLM(llmA))
	fromNil, err := a.RunFrom(context.Background(), nil, "hello")
	if err != nil {
		t.Fatalf("RunFrom(nil): %v", err)
	}

	llmB := eval.NewMockLLMClient(respWith("answer", 1, 1))
	b, _ := harness.New(harness.WithLLM(llmB))
	plain, err := b.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if fromNil.FinalOutput != plain.FinalOutput {
		t.Errorf("FinalOutput: RunFrom(nil)=%q, Run=%q", fromNil.FinalOutput, plain.FinalOutput)
	}
	if len(fromNil.Messages) != len(plain.Messages) {
		t.Errorf("len(Messages): RunFrom(nil)=%d, Run=%d", len(fromNil.Messages), len(plain.Messages))
	}
	if fromNil.DoneReason != plain.DoneReason {
		t.Errorf("DoneReason: RunFrom(nil)=%q, Run=%q", fromNil.DoneReason, plain.DoneReason)
	}
}

func TestRunFromCumulativeUsageAcrossTurns(t *testing.T) {
	llm := eval.NewMockLLMClient(
		respWith("a", 10, 5),
		respWith("b", 10, 5),
	)
	a, _ := harness.New(harness.WithLLM(llm))

	r1, err := a.RunFrom(context.Background(), nil, "turn 1")
	if err != nil {
		t.Fatalf("RunFrom 1: %v", err)
	}
	r2, err := a.RunFrom(context.Background(), r1, "turn 2")
	if err != nil {
		t.Fatalf("RunFrom 2: %v", err)
	}

	if r1.Usage.InputTokens != 10 {
		t.Errorf("r1 Usage.InputTokens = %d, want 10", r1.Usage.InputTokens)
	}
	if r2.Usage.InputTokens != 20 || r2.Usage.OutputTokens != 10 {
		t.Errorf("r2 Usage = %+v, want cumulative {20 10 0}", r2.Usage)
	}
}

func TestResumeTerminalIsNoOp(t *testing.T) {
	llm := eval.NewMockLLMClient(respWith("should not be used", 0, 0))
	a, _ := harness.New(harness.WithLLM(llm))

	prior := harness.NewState("done input")
	prior.Done = true
	prior.DoneReason = harness.DoneNoToolCalls
	prior.FinalOutput = "already final"

	got, err := a.Resume(context.Background(), prior)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got.FinalOutput != "already final" {
		t.Errorf("FinalOutput = %q, want unchanged %q", got.FinalOutput, "already final")
	}
	if len(llm.Requests()) != 0 {
		t.Errorf("LLM was called %d times on a terminal Resume; want 0", len(llm.Requests()))
	}
}

func TestResumeContinuesNonTerminal(t *testing.T) {
	llm := eval.NewMockLLMClient(respWith("resumed answer", 4, 4))
	a, _ := harness.New(harness.WithLLM(llm))

	prior := harness.NewState("orig")
	prior.Messages = []harness.Message{{Role: harness.RoleUser, Content: "orig"}}
	prior.Done = false

	got, err := a.Resume(context.Background(), prior)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !got.Done || got.DoneReason != harness.DoneNoToolCalls {
		t.Errorf("Done=%v DoneReason=%q, want true / %q", got.Done, got.DoneReason, harness.DoneNoToolCalls)
	}
	if got.FinalOutput != "resumed answer" {
		t.Errorf("FinalOutput = %q, want %q", got.FinalOutput, "resumed answer")
	}
	if len(llm.Requests()) != 1 {
		t.Errorf("LLM called %d times, want 1", len(llm.Requests()))
	}
}

func TestResumeNilPriorReturnsError(t *testing.T) {
	llm := eval.NewMockLLMClient(respWith("x", 0, 0))
	a, _ := harness.New(harness.WithLLM(llm))

	got, err := a.Resume(context.Background(), nil)
	if err == nil {
		t.Error("Resume(nil): want error, got nil")
	}
	if got == nil {
		t.Fatal("Resume(nil): want non-nil State (Run-family contract), got nil")
	}
	// The returned State should be a fresh empty one, not a partial stub.
	if got.Input != "" || got.Done || got.DoneReason != "" {
		t.Errorf("Resume(nil): want fresh empty State, got %+v", got)
	}
}

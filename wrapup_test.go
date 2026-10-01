package gantry_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/critic"
	"github.com/farazhassan/gantry/eval"
)

func toolTurn(id string) gantry.LLMResponse {
	return gantry.LLMResponse{
		ToolCalls:  []gantry.ToolCall{{ID: id, Name: "noop"}},
		StopReason: gantry.StopReasonToolUse,
	}
}

// advertiseNoop makes every request carry one ToolDef, so tests can assert the
// wrap-up request still lists tools (Anthropic requires that when the history
// holds tool calls) while forbidding their use via ToolChoiceNone.
func advertiseNoop(a *gantry.Agent) {
	a.Use(gantry.PhaseStart, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if err := next(ctx, s); err != nil {
				return err
			}
			s.Tools = append(s.Tools, gantry.ToolDef{Name: "noop", Description: "noop", Schema: json.RawMessage(`{}`)})
			return nil
		}
	})
}

func newCappedAgent(t *testing.T, mock *eval.MockLLMClient, max int) *gantry.Agent {
	t.Helper()
	a, err := gantry.NewAgent(gantry.WithLLM(mock), gantry.WithMaxIterations(max))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	advertiseNoop(a)
	return a
}

func TestWrapUpRunsToolLessTurnOnMaxIterations(t *testing.T) {
	mock := eval.NewMockLLMClient(
		toolTurn("a"),
		toolTurn("b"),
		gantry.LLMResponse{Content: "partial answer", StopReason: gantry.StopReasonEnd},
	)
	a := newCappedAgent(t, mock, 2)

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	reqs := mock.Requests()
	if len(reqs) != 3 {
		t.Fatalf("LLM calls = %d, want 3 (max 2 + wrap-up)", len(reqs))
	}
	for i := 0; i < 2; i++ {
		if reqs[i].ToolChoice != nil {
			t.Errorf("request %d ToolChoice = %+v, want nil", i, reqs[i].ToolChoice)
		}
	}
	last := reqs[2]
	if last.ToolChoice == nil || last.ToolChoice.Mode != gantry.ToolChoiceNone {
		t.Errorf("wrap-up ToolChoice = %+v, want Mode none", last.ToolChoice)
	}
	if len(last.Tools) != 1 {
		t.Errorf("wrap-up Tools = %d, want 1 (tools stay advertised)", len(last.Tools))
	}
	if m := last.Messages[len(last.Messages)-1]; m.Role != gantry.RoleUser || m.Content != gantry.MaxIterationsWrapUpPrompt {
		t.Errorf("wrap-up last message = %+v, want the wrap-up prompt", m)
	}
	if !state.Done || state.DoneReason != gantry.DoneMaxIterations {
		t.Errorf("Done/DoneReason = %v/%q, want true/%q", state.Done, state.DoneReason, gantry.DoneMaxIterations)
	}
	if state.FinalOutput != "partial answer" {
		t.Errorf("FinalOutput = %q, want %q", state.FinalOutput, "partial answer")
	}
	assertNoWrapUpPrompt(t, state)
	n := len(state.Messages)
	if m := state.Messages[n-1]; m.Role != gantry.RoleAssistant || m.Content != "partial answer" {
		t.Errorf("last message = %+v, want assistant 'partial answer'", m)
	}
	if state.Messages[n-2].Content == gantry.MaxIterationsWrapUpPrompt {
		t.Errorf("message before the answer is the wrap-up prompt")
	}
}

func TestWrapUpSkippedWhenRunFinishesBeforeCap(t *testing.T) {
	mock := eval.NewMockLLMClient(
		toolTurn("a"),
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a := newCappedAgent(t, mock, 5)

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	reqs := mock.Requests()
	if len(reqs) != 2 {
		t.Fatalf("LLM calls = %d, want 2", len(reqs))
	}
	for i, r := range reqs {
		if r.ToolChoice != nil {
			t.Errorf("request %d ToolChoice = %+v, want nil", i, r.ToolChoice)
		}
	}
	if state.DoneReason != gantry.DoneNoToolCalls || state.FinalOutput != "done" {
		t.Errorf("DoneReason/FinalOutput = %q/%q, want no_tool_calls/done", state.DoneReason, state.FinalOutput)
	}
}

func TestWrapUpDropsToolCallsTheModelMakesAnyway(t *testing.T) {
	mock := eval.NewMockLLMClient(
		toolTurn("a"),
		gantry.LLMResponse{
			Content:    "best guess",
			ToolCalls:  []gantry.ToolCall{{ID: "stray", Name: "noop"}},
			StopReason: gantry.StopReasonToolUse,
		},
	)
	a := newCappedAgent(t, mock, 1)

	var events []gantry.Event
	state, err := a.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		events = append(events, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if len(state.PendingToolCalls) != 0 {
		t.Errorf("PendingToolCalls = %+v, want none", state.PendingToolCalls)
	}
	last := state.Messages[len(state.Messages)-1]
	if last.Role != gantry.RoleAssistant || len(last.ToolCalls) != 0 || last.Content != "best guess" {
		t.Errorf("last message = %+v, want assistant 'best guess' with no tool calls", last)
	}
	if state.FinalOutput != "best guess" || state.DoneReason != gantry.DoneMaxIterations {
		t.Errorf("FinalOutput/DoneReason = %q/%q, want best guess/max_iterations", state.FinalOutput, state.DoneReason)
	}
	for _, ev := range events {
		if ev.Type == gantry.EventToolCall && ev.ToolCall != nil && ev.ToolCall.ID == "stray" {
			t.Errorf("stray wrap-up tool call was emitted as an event")
		}
	}
}

func TestWrapUpLLMErrorIsReturned(t *testing.T) {
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Response: toolTurn("a")},
		{Err: errors.New("wrap boom")},
	})
	a := newCappedAgent(t, mock, 1)

	state, err := a.Run(context.Background(), "go")
	if err == nil || !strings.Contains(err.Error(), "wrap boom") {
		t.Fatalf("err = %v, want it to carry 'wrap boom'", err)
	}
	if state == nil {
		t.Fatal("state = nil, want non-nil partial state")
	}
	assertNoWrapUpPrompt(t, state)
}

func TestWrapUpMiddlewareStopKeepsItsReason(t *testing.T) {
	// Only the capped turn is scripted: if the wrap-up reached the LLM the
	// mock would return ErrMockExhausted and Run would fail.
	mock := eval.NewMockLLMClient(toolTurn("a"))
	a := newCappedAgent(t, mock, 1)
	// Behaves like components/limiter's PhaseLLMCall pre-check, but only on
	// the wrap-up turn (the only call carrying a ToolChoice).
	a.Use(gantry.PhaseLLMCall, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if gantry.ToolChoiceFrom(ctx) != nil {
				s.Done = true
				s.DoneReason = gantry.DoneBudgetExceeded
				return nil
			}
			return next(ctx, s)
		}
	})

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.DoneReason != gantry.DoneBudgetExceeded {
		t.Errorf("DoneReason = %q, want %q", state.DoneReason, gantry.DoneBudgetExceeded)
	}
	if n := len(mock.Requests()); n != 1 {
		t.Errorf("LLM calls = %d, want 1 (wrap-up skipped)", n)
	}
}

func TestWrapUpSkippedWhenContextCancelled(t *testing.T) {
	mock := eval.NewMockLLMClient(toolTurn("a"), toolTurn("b"))
	a := newCappedAgent(t, mock, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancel during the last in-loop observe, so the loop exits on the cap
	// with a cancelled context.
	a.Use(gantry.PhaseObserve, func(next gantry.Handler) gantry.Handler {
		return func(c context.Context, s *gantry.State) error {
			if s.Iteration == 1 {
				cancel()
			}
			return next(c, s)
		}
	})

	state, err := a.Run(ctx, "go")
	if err != nil {
		t.Errorf("Run err = %v, want nil", err)
	}
	if n := len(mock.Requests()); n != 2 {
		t.Errorf("LLM calls = %d, want 2 (no wrap-up after cancel)", n)
	}
	if state.DoneReason != gantry.DoneMaxIterations {
		t.Errorf("DoneReason = %q, want %q", state.DoneReason, gantry.DoneMaxIterations)
	}
}

func TestWrapUpStreamsItsTurn(t *testing.T) {
	mock := eval.NewMockLLMClient(
		toolTurn("a"),
		gantry.LLMResponse{Content: "wrapped", StopReason: gantry.StopReasonEnd},
	)
	a := newCappedAgent(t, mock, 1)

	var events []gantry.Event
	_, err := a.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		events = append(events, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	var sawWrapUpLLMPhase bool
	var wrapText strings.Builder
	var done *gantry.Event
	for i := range events {
		ev := events[i]
		if ev.Type == gantry.EventPhaseStart && ev.Phase == gantry.PhaseLLMCall && ev.Iteration == 1 {
			sawWrapUpLLMPhase = true
		}
		if ev.Type == gantry.EventTextDelta && ev.Iteration == 1 {
			wrapText.WriteString(ev.TextDelta)
		}
		if ev.Type == gantry.EventDone {
			done = &events[i]
		}
	}
	if !sawWrapUpLLMPhase {
		t.Error("no llm_call phase_start at iteration 1 (the wrap-up turn)")
	}
	if wrapText.String() != "wrapped" {
		t.Errorf("wrap-up text deltas = %q, want %q", wrapText.String(), "wrapped")
	}
	if done == nil || done.FinalOutput != "wrapped" || done.DoneReason != gantry.DoneMaxIterations {
		t.Errorf("done event = %+v, want FinalOutput wrapped / max_iterations", done)
	}
}

func assertNoWrapUpPrompt(t *testing.T, s *gantry.State) {
	t.Helper()
	for _, m := range s.Messages {
		if m.Role == gantry.RoleUser && m.Content == gantry.MaxIterationsWrapUpPrompt {
			t.Errorf("wrap-up prompt found in stored transcript")
			return
		}
	}
}

func TestWrapUpPromptDoesNotLeakIntoNextTurn(t *testing.T) {
	mock := eval.NewMockLLMClient(
		toolTurn("a"),
		gantry.LLMResponse{Content: "capped answer", StopReason: gantry.StopReasonEnd},
		gantry.LLMResponse{Content: "second turn", StopReason: gantry.StopReasonEnd},
	)
	a := newCappedAgent(t, mock, 1)
	ctx := context.Background()

	first, err := a.Run(ctx, "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err = a.RunFrom(ctx, first, "follow up"); err != nil {
		t.Fatalf("RunFrom: %v", err)
	}
	reqs := mock.Requests()
	if len(reqs) != 3 {
		t.Fatalf("LLM calls = %d, want 3", len(reqs))
	}
	msgs := reqs[2].Messages
	for _, m := range msgs {
		if m.Content == gantry.MaxIterationsWrapUpPrompt {
			t.Errorf("wrap-up prompt leaked into the next turn's request")
		}
	}
	if m := msgs[len(msgs)-1]; m.Role != gantry.RoleUser || m.Content != "follow up" {
		t.Errorf("last message = %+v, want user 'follow up'", m)
	}
}

func TestWrapUpStrayToolCallsWithEmptyContent(t *testing.T) {
	mock := eval.NewMockLLMClient(
		toolTurn("a"),
		gantry.LLMResponse{ToolCalls: []gantry.ToolCall{{ID: "stray", Name: "noop"}}, StopReason: gantry.StopReasonToolUse},
	)
	a := newCappedAgent(t, mock, 1)

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.FinalOutput != "" || state.DoneReason != gantry.DoneMaxIterations {
		t.Errorf("FinalOutput/DoneReason = %q/%q, want empty/max_iterations", state.FinalOutput, state.DoneReason)
	}
	if len(state.PendingToolCalls) != 0 {
		t.Errorf("PendingToolCalls = %+v, want none", state.PendingToolCalls)
	}
	if last := state.Messages[len(state.Messages)-1]; len(last.ToolCalls) != 0 {
		t.Errorf("last message still has tool calls: %+v", last)
	}
}

type wrapUpRejectingCritic struct{}

func (wrapUpRejectingCritic) Critique(ctx context.Context, _ *gantry.State) (critic.Verdict, error) {
	if gantry.ToolChoiceFrom(ctx) != nil {
		return critic.Verdict{Accept: false, Reason: "no"}, nil
	}
	return critic.Verdict{Accept: true}, nil
}

func TestWrapUpCriticRejectionLeavesEmptyOutput(t *testing.T) {
	mock := eval.NewMockLLMClient(
		toolTurn("a"),
		gantry.LLMResponse{Content: "draft", StopReason: gantry.StopReasonEnd},
	)
	a := newCappedAgent(t, mock, 1)
	a.With(critic.New(wrapUpRejectingCritic{}))

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.DoneReason != gantry.DoneMaxIterations || state.FinalOutput != "" {
		t.Errorf("DoneReason/FinalOutput = %q/%q, want max_iterations/empty", state.DoneReason, state.FinalOutput)
	}
	assertNoWrapUpPrompt(t, state)
}

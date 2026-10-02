package gantry_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/checkpointer"
	ckmem "github.com/farazhassan/gantry/components/checkpointer/mem"
	"github.com/farazhassan/gantry/components/compactor"
	"github.com/farazhassan/gantry/components/critic"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/components/transcript"
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
	assertTextlessWrapUp(t, state)
}

func TestWrapUpEmptyTextAnswer(t *testing.T) {
	mock := eval.NewMockLLMClient(
		toolTurn("a"),
		gantry.LLMResponse{Content: "", StopReason: gantry.StopReasonEnd},
	)
	a := newCappedAgent(t, mock, 1)

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertTextlessWrapUp(t, state)
}

// assertTextlessWrapUp checks a run whose wrap-up turn produced no text: the
// run is unanswered (empty FinalOutput, max_iterations), and the transcript
// ends with the placeholder assistant message instead of an empty one.
func assertTextlessWrapUp(t *testing.T, state *gantry.State) {
	t.Helper()
	if state.FinalOutput != "" || state.DoneReason != gantry.DoneMaxIterations {
		t.Errorf("FinalOutput/DoneReason = %q/%q, want empty/max_iterations", state.FinalOutput, state.DoneReason)
	}
	if len(state.PendingToolCalls) != 0 {
		t.Errorf("PendingToolCalls = %+v, want none", state.PendingToolCalls)
	}
	for _, m := range state.Messages {
		if m.Role == gantry.RoleAssistant && m.Content == "" && len(m.ToolCalls) == 0 {
			t.Errorf("empty assistant message left in transcript: %+v", m)
		}
		for _, tc := range m.ToolCalls {
			if tc.ID == "stray" {
				t.Errorf("stray tool call left in transcript")
			}
		}
	}
	last := state.Messages[len(state.Messages)-1]
	if last.Role != gantry.RoleAssistant || last.Content != gantry.WrapUpNoAnswer || len(last.ToolCalls) != 0 {
		t.Errorf("last message = %+v, want assistant placeholder %q with no tool calls", last, gantry.WrapUpNoAnswer)
	}
}

func TestWrapUpTextlessAnswerPersistsPlaceholder(t *testing.T) {
	cases := map[string]gantry.LLMResponse{
		"stray_calls": {ToolCalls: []gantry.ToolCall{{ID: "stray", Name: "noop"}}, StopReason: gantry.StopReasonToolUse},
		"plain_empty": {Content: "", StopReason: gantry.StopReasonEnd},
	}
	for name, wrap := range cases {
		t.Run(name, func(t *testing.T) {
			mock := eval.NewMockLLMClient(toolTurn("a"), wrap)
			a, err := gantry.NewAgent(gantry.WithLLM(mock), gantry.WithMaxIterations(1))
			if err != nil {
				t.Fatalf("NewAgent: %v", err)
			}
			if err := a.With(tool.FromTools(1, wrapUpNoopTool{})); err != nil {
				t.Fatalf("install tool: %v", err)
			}
			store := transcript.NewInMemoryStore()
			if err := a.With(transcript.New(store)); err != nil {
				t.Fatalf("install transcript: %v", err)
			}

			if _, err := a.Run(context.Background(), "go"); err != nil {
				t.Fatalf("Run: %v", err)
			}
			hist, err := store.Read(context.Background())
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			toolUse := map[string]int{}
			for _, m := range hist {
				if m.Role != gantry.RoleAssistant {
					continue
				}
				if m.Content == "" && len(m.ToolCalls) == 0 {
					t.Errorf("stored an empty assistant message: %+v", m)
				}
				for _, tc := range m.ToolCalls {
					toolUse[tc.ID]++
				}
			}
			for id, n := range toolUse {
				if n != 1 {
					t.Errorf("tool_use %q stored %d times, want 1 (history %+v)", id, n, hist)
				}
			}
			if last := hist[len(hist)-1]; last.Role != gantry.RoleAssistant || last.Content != gantry.WrapUpNoAnswer {
				t.Errorf("last stored message = %+v, want the placeholder", last)
			}
		})
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
	if err := a.With(critic.New(wrapUpRejectingCritic{})); err != nil {
		t.Fatal(err)
	}

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.DoneReason != gantry.DoneMaxIterations || state.FinalOutput != "" {
		t.Errorf("DoneReason/FinalOutput = %q/%q, want max_iterations/empty", state.DoneReason, state.FinalOutput)
	}
	assertNoWrapUpPrompt(t, state)
}

func TestWrapUpResumedCheckpointDoesNotDuplicatePrompt(t *testing.T) {
	mock := eval.NewMockLLMClient(
		toolTurn("a"),
		gantry.LLMResponse{Content: "first wrap", StopReason: gantry.StopReasonEnd},
		gantry.LLMResponse{Content: "resumed wrap", StopReason: gantry.StopReasonEnd},
	)
	a := newCappedAgent(t, mock, 1)
	ctx := context.Background()

	st, err := a.Run(ctx, "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Simulate a checkpoint taken mid-wrap-up: prompt present, not done.
	st.Done = false
	st.DoneReason = ""
	st.Messages = append(st.Messages, gantry.Message{Role: gantry.RoleUser, Content: gantry.MaxIterationsWrapUpPrompt})

	final, err := a.Resume(ctx, st)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	reqs := mock.Requests()
	last := reqs[len(reqs)-1]
	count := 0
	for _, m := range last.Messages {
		if m.Content == gantry.MaxIterationsWrapUpPrompt {
			count++
		}
	}
	if count != 1 {
		t.Errorf("wrap-up request has %d prompt messages, want 1", count)
	}
	assertNoWrapUpPrompt(t, final)
}

// wrapUpNoopTool lets the capped turn's tool call dispatch, so its result lands
// in the transcript ahead of the wrap-up turn.
type wrapUpNoopTool struct{}

func (wrapUpNoopTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{Name: "noop", Description: "noop", Schema: json.RawMessage(`{}`)}
}

func (wrapUpNoopTool) Invoke(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`"ok"`), nil
}

func TestWrapUpPromptAppendedAfterContextAssembly(t *testing.T) {
	mock := eval.NewMockLLMClient(
		toolTurn("a"),
		gantry.LLMResponse{Content: "wrapped", StopReason: gantry.StopReasonEnd},
	)
	a, err := gantry.NewAgent(gantry.WithLLM(mock), gantry.WithMaxIterations(1))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if err := a.With(tool.FromTools(1, wrapUpNoopTool{})); err != nil {
		t.Fatalf("install tool: %v", err)
	}
	if err := a.With(compactor.New(compactor.NewSlidingWindow(2), compactor.Budget{})); err != nil {
		t.Fatalf("install compactor: %v", err)
	}

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	reqs := mock.Requests()
	if len(reqs) != 2 {
		t.Fatalf("LLM calls = %d, want 2", len(reqs))
	}
	msgs := reqs[1].Messages
	if len(msgs) != 3 {
		t.Fatalf("wrap-up request messages = %+v, want [assistant tool call, tool result, prompt]", msgs)
	}
	if m := msgs[0]; m.Role != gantry.RoleAssistant || len(m.ToolCalls) != 1 || m.ToolCalls[0].ID != "a" {
		t.Errorf("wrap-up msgs[0] = %+v, want the assistant tool call", m)
	}
	if m := msgs[1]; m.Role != gantry.RoleTool || m.ToolCallID != "a" {
		t.Errorf("wrap-up msgs[1] = %+v, want the tool result for 'a'", m)
	}
	if m := msgs[2]; m.Role != gantry.RoleUser || m.Content != gantry.MaxIterationsWrapUpPrompt {
		t.Errorf("wrap-up msgs[2] = %+v, want the wrap-up prompt", m)
	}

	assertNoWrapUpPrompt(t, state)
	n := len(state.Messages)
	if n < 3 {
		t.Fatalf("stored messages = %+v, want at least 3", state.Messages)
	}
	if m := state.Messages[n-3]; m.Role != gantry.RoleAssistant || len(m.ToolCalls) != 1 || m.ToolCalls[0].ID != "a" {
		t.Errorf("stored msgs[n-3] = %+v, want the assistant tool call", m)
	}
	if m := state.Messages[n-2]; m.Role != gantry.RoleTool || m.ToolCallID != "a" {
		t.Errorf("stored msgs[n-2] = %+v, want the tool result for 'a'", m)
	}
	if m := state.Messages[n-1]; m.Role != gantry.RoleAssistant || m.Content != "wrapped" {
		t.Errorf("stored last message = %+v, want assistant 'wrapped'", m)
	}
}

func TestWrapUpPostLLMMiddlewareSeesStrippedAnswer(t *testing.T) {
	mock := eval.NewMockLLMClient(
		toolTurn("a"),
		gantry.LLMResponse{
			Content:    "best guess",
			ToolCalls:  []gantry.ToolCall{{ID: "stray", Name: "noop"}},
			StopReason: gantry.StopReasonToolUse,
		},
	)
	a := newCappedAgent(t, mock, 1)
	type seen struct {
		calls, pending int
		done           bool
		output         string
		reason         gantry.DoneReason
		prompt         bool
	}
	var got *seen
	a.Use(gantry.PhasePostLLM, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if err := next(ctx, s); err != nil {
				return err
			}
			if gantry.ToolChoiceFrom(ctx) != nil {
				got = &seen{
					calls:   len(s.LastResponse.ToolCalls),
					pending: len(s.PendingToolCalls),
					done:    s.Done,
					output:  s.FinalOutput,
					reason:  s.DoneReason,
					prompt:  hasWrapUpPrompt(s),
				}
			}
			return nil
		}
	})

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got == nil {
		t.Fatal("post_llm middleware never ran on the wrap-up turn")
	}
	if got.calls != 0 || got.pending != 0 || !got.done || got.output != "best guess" {
		t.Errorf("post_llm saw %+v, want 0 calls, 0 pending, done, output 'best guess'", *got)
	}
	if got.reason != gantry.DoneMaxIterations || got.prompt {
		t.Errorf("post_llm saw reason %q / prompt %v, want %q / no prompt", got.reason, got.prompt, gantry.DoneMaxIterations)
	}
}

func hasWrapUpPrompt(s *gantry.State) bool {
	for _, m := range s.Messages {
		if m.Role == gantry.RoleUser && m.Content == gantry.MaxIterationsWrapUpPrompt {
			return true
		}
	}
	return false
}

func TestWrapUpPostLLMCheckpointIsTerminalAndClean(t *testing.T) {
	mock := eval.NewMockLLMClient(
		toolTurn("a"),
		gantry.LLMResponse{Content: "wrapped", StopReason: gantry.StopReasonEnd},
	)
	a, err := gantry.NewAgent(gantry.WithLLM(mock), gantry.WithMaxIterations(1))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if err := a.With(tool.FromTools(1, wrapUpNoopTool{})); err != nil {
		t.Fatalf("install tool: %v", err)
	}
	// Simulate a crash before PhaseEnd's save: registered before the
	// checkpointer, so it is inner to its PhaseEnd hook, which then skips
	// saving. The stored checkpoint is the wrap-up turn's PhasePostLLM save.
	crash := errors.New("crash before end")
	a.Use(gantry.PhaseEnd, func(gantry.Handler) gantry.Handler {
		return func(context.Context, *gantry.State) error { return crash }
	})
	cp := ckmem.New()
	if err := a.With(checkpointer.New(cp, "wrap", gantry.PhasePostLLM)); err != nil {
		t.Fatalf("install checkpointer: %v", err)
	}

	if _, err := a.Run(context.Background(), "go"); !errors.Is(err, crash) {
		t.Fatalf("Run err = %v, want the simulated crash", err)
	}
	loaded, err := cp.Load(context.Background(), "wrap")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.Done || loaded.DoneReason != gantry.DoneMaxIterations || loaded.FinalOutput != "wrapped" {
		t.Errorf("checkpoint Done/DoneReason/FinalOutput = %v/%q/%q, want true/%q/wrapped",
			loaded.Done, loaded.DoneReason, loaded.FinalOutput, gantry.DoneMaxIterations)
	}
	if hasWrapUpPrompt(loaded) {
		t.Errorf("checkpoint saved at post_llm still holds the wrap-up prompt")
	}
}

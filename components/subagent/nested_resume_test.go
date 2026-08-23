// components/subagent/nested_resume_test.go
package subagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/eval"
)

func TestOneLevelNestedSuspendAndResume(t *testing.T) {
	childMock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "ask1", Name: "ask_user", Input: json.RawMessage(`{"q":"proceed?"}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "plan executed", StopReason: gantry.StopReasonEnd},
	)
	child, err := gantry.NewAgent(gantry.WithLLM(childMock))
	if err != nil {
		t.Fatalf("NewAgent(child): %v", err)
	}
	if err := child.With(tool.Client(gantry.ToolDef{Name: "ask_user", Description: "d", Schema: json.RawMessage(`{}`)})); err != nil {
		t.Fatalf("install client tools on child: %v", err)
	}
	delegate := New("planner", "plans, asking before executing", child)

	parentMock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "planner", Input: json.RawMessage(`{"goal":"ship it"}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "the planner reports: plan executed", StopReason: gantry.StopReasonEnd},
	)
	parentComp, parentReg := ComponentWithRegistry(1, delegate)
	parent, err := gantry.NewAgent(gantry.WithLLM(parentMock), gantry.WithComponents(parentComp))
	if err != nil {
		t.Fatalf("NewAgent(parent): %v", err)
	}

	suspended, err := parent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !suspended.Done || suspended.DoneReason != gantry.DoneClientToolCall {
		t.Fatalf("parent Done=%v DoneReason=%q, want suspended by the child's ask_user call", suspended.Done, suspended.DoneReason)
	}
	if len(suspended.PendingToolCalls) != 1 || suspended.PendingToolCalls[0].Name != "ask_user" {
		t.Fatalf("parent PendingToolCalls = %#v, want the child's ask_user call surfaced", suspended.PendingToolCalls)
	}
	for _, m := range suspended.Messages {
		if m.ToolCallID == "c1" {
			t.Errorf("originating planner call c1 must not have a folded result while its child is pending")
		}
	}

	final, err := tool.Resume(context.Background(), parent, parentReg, suspended, []gantry.ToolResult{
		{CallID: suspended.PendingToolCalls[0].ID, Content: `{"answer":"yes"}`},
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if final.DoneReason != gantry.DoneNoToolCalls || final.FinalOutput != "the planner reports: plan executed" {
		t.Fatalf("Done=%q out=%q, want the parent to finish normally using the planner's completed output", final.DoneReason, final.FinalOutput)
	}
	if len(parentMock.Requests()) != 2 {
		t.Fatalf("parent LLM called %d times, want 2 (dispatch + continuation after resume)", len(parentMock.Requests()))
	}
	if len(childMock.Requests()) != 2 {
		t.Fatalf("child LLM called %d times, want 2 (initial call + continuation after its own answer)", len(childMock.Requests()))
	}
}

func TestTwoSiblingDelegateCallsSuspendAndResumeIndependently(t *testing.T) {
	newAskingChild := func(t *testing.T, question string) (*gantry.Agent, *eval.MockLLMClient) {
		t.Helper()
		mock := eval.NewMockLLMClient(
			gantry.LLMResponse{
				ToolCalls:  []gantry.ToolCall{{ID: "ask1", Name: "ask_user", Input: json.RawMessage(`{"q":"` + question + `"}`)}},
				StopReason: gantry.StopReasonToolUse,
			},
			gantry.LLMResponse{Content: "done: " + question, StopReason: gantry.StopReasonEnd},
		)
		c, err := gantry.NewAgent(gantry.WithLLM(mock))
		if err != nil {
			t.Fatalf("NewAgent: %v", err)
		}
		if err := c.With(tool.Client(gantry.ToolDef{Name: "ask_user", Description: "d", Schema: json.RawMessage(`{}`)})); err != nil {
			t.Fatalf("install client tools: %v", err)
		}
		return c, mock
	}

	childA, mockA := newAskingChild(t, "A?")
	childB, mockB := newAskingChild(t, "B?")
	delegateA := New("agentA", "d", childA)
	delegateB := New("agentB", "d", childB)

	parentMock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "c1", Name: "agentA", Input: json.RawMessage(`{"goal":"a"}`)},
				{ID: "c2", Name: "agentB", Input: json.RawMessage(`{"goal":"b"}`)},
			},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "both done", StopReason: gantry.StopReasonEnd},
	)
	parentComp, parentReg := ComponentWithRegistry(2, delegateA, delegateB)
	parent, err := gantry.NewAgent(gantry.WithLLM(parentMock), gantry.WithComponents(parentComp))
	if err != nil {
		t.Fatalf("NewAgent(parent): %v", err)
	}

	suspended, err := parent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(suspended.PendingToolCalls) != 2 {
		t.Fatalf("PendingToolCalls = %#v, want 2 (one per sibling delegate)", suspended.PendingToolCalls)
	}
	for _, m := range suspended.Messages {
		if m.ToolCallID == "c1" || m.ToolCallID == "c2" {
			t.Errorf("originating delegate call %s must not have a folded result while its child is pending", m.ToolCallID)
		}
	}

	// Give each sibling a distinguishable answer so that a routing bug that
	// cross-wires the two children's answers is actually detectable below,
	// rather than being masked by both children being handed the same
	// content.
	const answerA = `{"answer":"yes-for-A"}`
	const answerB = `{"answer":"yes-for-B"}`
	answers := make([]gantry.ToolResult, len(suspended.PendingToolCalls))
	for i, pc := range suspended.PendingToolCalls {
		switch {
		case bytes.Contains(pc.Input, []byte("A?")):
			answers[i] = gantry.ToolResult{CallID: pc.ID, Content: answerA}
		case bytes.Contains(pc.Input, []byte("B?")):
			answers[i] = gantry.ToolResult{CallID: pc.ID, Content: answerB}
		default:
			t.Fatalf("pending call %#v does not match either sibling's question, cannot assign an answer", pc)
		}
	}
	final, err := tool.Resume(context.Background(), parent, parentReg, suspended, answers)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if final.DoneReason != gantry.DoneNoToolCalls || final.FinalOutput != "both done" {
		t.Fatalf("Done=%q out=%q, want both siblings to finish and the parent to continue", final.DoneReason, final.FinalOutput)
	}

	// Prove the answers were not cross-wired: each child's own second LLM
	// request (its continuation after being resumed) must carry the
	// tool-result message that was actually addressed to it, and must not
	// carry the sibling's answer instead.
	requireOwnAnswer := func(t *testing.T, who string, mock *eval.MockLLMClient, want string) {
		t.Helper()
		reqs := mock.Requests()
		if len(reqs) != 2 {
			t.Fatalf("%s LLM called %d times, want 2 (initial call + continuation after its own answer)", who, len(reqs))
		}
		var got string
		found := false
		for _, m := range reqs[1].Messages {
			if m.Role == gantry.RoleTool && m.ToolCallID == "ask1" {
				got = m.Content
				found = true
			}
		}
		if !found {
			t.Fatalf("%s second LLM request has no RoleTool message for ask1; messages=%#v", who, reqs[1].Messages)
		}
		if got != want {
			t.Errorf("%s received answer %q, want %q (its own answer, not its sibling's)", who, got, want)
		}
	}
	requireOwnAnswer(t, "childA", mockA, answerA)
	requireOwnAnswer(t, "childB", mockB, answerB)
}

func TestTwoLevelNestedSuspendAndResume(t *testing.T) {
	grandchildMock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "ask1", Name: "ask_user", Input: json.RawMessage(`{"q":"deep question?"}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "grandchild done", StopReason: gantry.StopReasonEnd},
	)
	grandchild, err := gantry.NewAgent(gantry.WithLLM(grandchildMock))
	if err != nil {
		t.Fatalf("NewAgent(grandchild): %v", err)
	}
	if err := grandchild.With(tool.Client(gantry.ToolDef{Name: "ask_user", Description: "d", Schema: json.RawMessage(`{}`)})); err != nil {
		t.Fatalf("install client tools on grandchild: %v", err)
	}
	deepDelegate := New("deep_specialist", "d", grandchild)

	childMock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "g1", Name: "deep_specialist", Input: json.RawMessage(`{"goal":"dig deeper"}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "child used the grandchild's answer", StopReason: gantry.StopReasonEnd},
	)
	childComp, childReg := ComponentWithRegistry(1, deepDelegate)
	child, err := gantry.NewAgent(gantry.WithLLM(childMock), gantry.WithComponents(childComp))
	if err != nil {
		t.Fatalf("NewAgent(child): %v", err)
	}
	delegate := New("specialist", "d", child, WithChildRegistry(childReg))

	parentMock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "specialist", Input: json.RawMessage(`{"goal":"go deep"}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "parent finished", StopReason: gantry.StopReasonEnd},
	)
	parentComp, parentReg := ComponentWithRegistry(1, delegate)
	parent, err := gantry.NewAgent(gantry.WithLLM(parentMock), gantry.WithComponents(parentComp))
	if err != nil {
		t.Fatalf("NewAgent(parent): %v", err)
	}

	suspended, err := parent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !suspended.Done || suspended.DoneReason != gantry.DoneClientToolCall {
		t.Fatalf("Done=%v DoneReason=%q, want suspended by the grandchild's ask_user call", suspended.Done, suspended.DoneReason)
	}
	if len(suspended.PendingToolCalls) != 1 || suspended.PendingToolCalls[0].Name != "ask_user" {
		t.Fatalf("PendingToolCalls = %#v, want the grandchild's ask_user call surfaced through two levels", suspended.PendingToolCalls)
	}

	final, err := tool.Resume(context.Background(), parent, parentReg, suspended, []gantry.ToolResult{
		{CallID: suspended.PendingToolCalls[0].ID, Content: `{"answer":"the deep answer"}`},
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if final.DoneReason != gantry.DoneNoToolCalls || final.FinalOutput != "parent finished" {
		t.Fatalf("Done=%q out=%q, want the parent to finish normally after resuming through two nested levels", final.DoneReason, final.FinalOutput)
	}
	if len(grandchildMock.Requests()) != 2 {
		t.Errorf("grandchild LLM called %d times, want 2", len(grandchildMock.Requests()))
	}
	if len(childMock.Requests()) != 2 {
		t.Errorf("child LLM called %d times, want 2", len(childMock.Requests()))
	}
	if len(parentMock.Requests()) != 2 {
		t.Errorf("parent LLM called %d times, want 2", len(parentMock.Requests()))
	}

	// Prove the answer actually reached the grandchild's second LLM turn
	// intact, rather than merely producing matching call counts while being
	// dropped, corrupted, or misrouted along the two-level path back down.
	grandchildReqs := grandchildMock.Requests()
	if len(grandchildReqs) < 2 {
		t.Fatalf("grandchild LLM called %d times, want at least 2", len(grandchildReqs))
	}
	var got string
	found := false
	for _, m := range grandchildReqs[1].Messages {
		if m.Role == gantry.RoleTool && m.ToolCallID == "ask1" {
			got = m.Content
			found = true
		}
	}
	if !found {
		t.Fatalf("grandchild second LLM request has no RoleTool message for ask1; messages=%#v", grandchildReqs[1].Messages)
	}
	const wantAnswer = `{"answer":"the deep answer"}`
	if got != wantAnswer {
		t.Errorf("grandchild received answer %q, want %q (content preserved through two nested levels)", got, wantAnswer)
	}
}

func TestResumeReappliesDepthLimitToFurtherNestingAfterResuming(t *testing.T) {
	// A resumed child that goes on to dispatch a NEW grandchild past
	// WithMaxDepth must still be refused — depth tracking must survive the
	// suspend/resume boundary (the original dispatch's depth-carrying ctx
	// does not, since Invoke already returned once the child suspended).
	//
	// tooDeepMock is scripted with a response it must NEVER reach: if depth
	// tracking wrongly reset to 0 across the resume boundary, the refusal
	// check (depth >= maxDepth) would not fire and this mock would actually
	// run and succeed, silently masking the regression this test exists to
	// catch — a scriptless mock would fail either way (refused, or merely
	// exhausted) and couldn't tell the two apart.
	tooDeepMock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "should never run", StopReason: gantry.StopReasonEnd})
	tooDeepChild, err := gantry.NewAgent(gantry.WithLLM(tooDeepMock))
	if err != nil {
		t.Fatalf("NewAgent(tooDeepChild): %v", err)
	}
	blockedDelegate := New("blocked", "d", tooDeepChild, WithMaxDepth(1))

	childMock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "ask1", Name: "ask_user", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{
			// After resuming, the child itself tries to go one level deeper
			// than its own WithMaxDepth(1) allows.
			ToolCalls:  []gantry.ToolCall{{ID: "g1", Name: "blocked", Input: json.RawMessage(`{"goal":"too deep"}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "recovered after the depth error", StopReason: gantry.StopReasonEnd},
	)
	childComp, childReg := ComponentWithRegistry(1, blockedDelegate)
	child, err := gantry.NewAgent(gantry.WithLLM(childMock), gantry.WithComponents(childComp))
	if err != nil {
		t.Fatalf("NewAgent(child): %v", err)
	}
	if err := child.With(tool.Client(gantry.ToolDef{Name: "ask_user", Description: "d", Schema: json.RawMessage(`{}`)})); err != nil {
		t.Fatalf("install client tools on child: %v", err)
	}
	delegate := New("specialist", "d", child, WithChildRegistry(childReg), WithMaxDepth(1))

	parentMock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "specialist", Input: json.RawMessage(`{"goal":"go"}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "parent finished", StopReason: gantry.StopReasonEnd},
	)
	parentComp, parentReg := ComponentWithRegistry(1, delegate)
	parent, err := gantry.NewAgent(gantry.WithLLM(parentMock), gantry.WithComponents(parentComp))
	if err != nil {
		t.Fatalf("NewAgent(parent): %v", err)
	}

	suspended, err := parent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	final, err := tool.Resume(context.Background(), parent, parentReg, suspended, []gantry.ToolResult{
		{CallID: suspended.PendingToolCalls[0].ID, Content: `{"answer":"ok"}`},
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if final.DoneReason != gantry.DoneNoToolCalls || final.FinalOutput != "parent finished" {
		t.Fatalf("Done=%q out=%q, want the run to recover past the depth-limit tool error to a normal finish", final.DoneReason, final.FinalOutput)
	}
	var sawDepthError bool
	for _, m := range childMock.Requests()[len(childMock.Requests())-1].Messages {
		if m.Role == gantry.RoleTool && m.ToolCallID == "g1" {
			sawDepthError = true
			if !strings.Contains(m.Content, "depth limit reached") {
				t.Errorf("g1 result content = %q, want it to specifically name the depth limit (proves depth, not something else, was enforced)", m.Content)
			}
		}
	}
	if !sawDepthError {
		t.Error("expected the depth-limited grandchild call to fold as an error result, proving the limit was actually enforced on resume")
	}
	if len(tooDeepMock.Requests()) != 0 {
		t.Errorf("tooDeepChild received %d LLM requests, want 0 (must be refused before ever running — see tooDeepMock's comment above)", len(tooDeepMock.Requests()))
	}
}

// suspendThenBlockLLM answers the first Generate call with a suspending
// ask_user call, then blocks on every subsequent call until ctx is done — a
// stand-in for a hung provider on the child's post-resume turn, mirroring
// runctx_test.go's blockingLLM but only from the second call onward so the
// first (suspending) turn completes normally.
type suspendThenBlockLLM struct {
	mu    sync.Mutex
	calls int
}

func (l *suspendThenBlockLLM) Generate(ctx context.Context, _ gantry.LLMRequest) (gantry.LLMResponse, error) {
	l.mu.Lock()
	l.calls++
	first := l.calls == 1
	l.mu.Unlock()
	if first {
		return gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "ask1", Name: "ask_user", Input: json.RawMessage(`{"q":"proceed?"}`)}},
			StopReason: gantry.StopReasonToolUse,
		}, nil
	}
	<-ctx.Done()
	return gantry.LLMResponse{}, ctx.Err()
}

func TestWithTimeoutAppliesToResumeLegNotJustInvoke(t *testing.T) {
	// Mirrors TestWithTimeoutCancelsChildRun (runctx_test.go) but on the
	// Resume leg: the child suspends on ask_user (no timeout involved yet),
	// then the RESUME leg's own child work hangs and must be cut off by the
	// same WithTimeout duration. This proves the timeout is genuinely
	// re-applied on Resume — not merely remembered from Invoke (which would
	// incorrectly let a slow resume run forever) and not inherited from
	// Invoke's now-cancelled context (which would incorrectly kill Resume
	// immediately even with plenty of time left).
	child, err := gantry.NewAgent(gantry.WithLLM(&suspendThenBlockLLM{}))
	if err != nil {
		t.Fatalf("NewAgent(child): %v", err)
	}
	if err := child.With(tool.Client(gantry.ToolDef{Name: "ask_user", Description: "d", Schema: json.RawMessage(`{}`)})); err != nil {
		t.Fatalf("install client tools on child: %v", err)
	}
	tl := New("slow_specialist", "d", child, WithTimeout(30*time.Millisecond))
	rt, ok := tl.(tool.ResumableTool)
	if !ok {
		t.Fatalf("delegate tool does not implement tool.ResumableTool")
	}

	_, err = tl.Invoke(context.Background(), json.RawMessage(`{"goal":"g"}`))
	var pending *gantry.PendingResult
	if !errors.As(err, &pending) {
		t.Fatalf("Invoke err = %v, want a *gantry.PendingResult (child asked a question)", err)
	}

	_, err = rt.Resume(context.Background(), pending.Resume, []gantry.ToolResult{
		{CallID: pending.Pending[0].ID, Content: `{"answer":"yes"}`},
	})
	if err == nil {
		t.Fatalf("Resume = nil error, want a deadline error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want errors.Is(err, context.DeadlineExceeded)", err)
	}
}

func TestEventPassthroughAndScopingParityAcrossResume(t *testing.T) {
	// Mirrors TestWithEventPassthroughKeepsAmbientSink and
	// TestChildEventsScopedOutByDefault (runctx_test.go), but exercises the
	// Resume leg after a suspend rather than a plain one-shot Invoke: proves
	// both the opt-in passthrough and the scoped-out default are re-applied
	// on Resume, matching Invoke's behavior instead of being forgotten (or
	// wrongly left enabled) once the run continues past a suspend.
	newAskingChild := func(t *testing.T) *gantry.Agent {
		t.Helper()
		mock := eval.NewMockLLMClient(
			gantry.LLMResponse{
				ToolCalls:  []gantry.ToolCall{{ID: "ask1", Name: "ask_user", Input: json.RawMessage(`{"q":"proceed?"}`)}},
				StopReason: gantry.StopReasonToolUse,
			},
			gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
		)
		c, err := gantry.NewAgent(gantry.WithLLM(mock))
		if err != nil {
			t.Fatalf("NewAgent: %v", err)
		}
		if err := c.With(tool.Client(gantry.ToolDef{Name: "ask_user", Description: "d", Schema: json.RawMessage(`{}`)})); err != nil {
			t.Fatalf("install client tools: %v", err)
		}
		return c
	}

	// suspend runs Invoke to the point of suspension and returns the
	// PendingResult plus the tool cast to ResumableTool for the Resume call.
	suspend := func(t *testing.T, tl tool.Tool, ctx context.Context) (*gantry.PendingResult, tool.ResumableTool) {
		t.Helper()
		rt, ok := tl.(tool.ResumableTool)
		if !ok {
			t.Fatalf("delegate tool does not implement tool.ResumableTool")
		}
		_, err := tl.Invoke(ctx, json.RawMessage(`{"goal":"g"}`))
		var pending *gantry.PendingResult
		if !errors.As(err, &pending) {
			t.Fatalf("Invoke err = %v, want a *gantry.PendingResult (child asked a question)", err)
		}
		return pending, rt
	}

	t.Run("passthrough survives resume", func(t *testing.T) {
		tl := New("specialist", "d", newAskingChild(t), WithEventPassthrough())

		var events int
		ctx := gantry.WithSink(context.Background(), func(gantry.Event) error {
			events++
			return nil
		})
		pending, rt := suspend(t, tl, ctx)

		// Reset the counter so what follows measures only the Resume leg's
		// own contribution, isolated from whatever Invoke already produced
		// before the suspend.
		events = 0
		if _, err := rt.Resume(ctx, pending.Resume, []gantry.ToolResult{
			{CallID: pending.Pending[0].ID, Content: `{"answer":"yes"}`},
		}); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if events == 0 {
			t.Errorf("ambient sink saw no events from the Resume leg, want phase/done events with passthrough")
		}
	})

	t.Run("scoped out by default on resume", func(t *testing.T) {
		tl := New("specialist", "d", newAskingChild(t))

		var events int
		ctx := gantry.WithSink(context.Background(), func(gantry.Event) error {
			events++
			return nil
		})
		pending, rt := suspend(t, tl, ctx)

		events = 0
		if _, err := rt.Resume(ctx, pending.Resume, []gantry.ToolResult{
			{CallID: pending.Pending[0].ID, Content: `{"answer":"yes"}`},
		}); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if events != 0 {
			t.Errorf("ambient sink saw %d events from the Resume leg, want 0 (scoped out by default, matching Invoke)", events)
		}
	})
}

package task

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/critic"
)

// stubCritic returns a fixed verdict/error and records the State it received.
type stubCritic struct {
	verdict critic.Verdict
	err     error
	called  bool
	gotMsgs []gantry.Message
	gotPlan *gantry.Plan
	gotLast *gantry.LLMResponse
}

func (s *stubCritic) Critique(_ context.Context, st *gantry.State) (critic.Verdict, error) {
	s.called = true
	s.gotMsgs = st.Messages
	s.gotPlan = st.Plan
	s.gotLast = st.LastResponse
	return s.verdict, s.err
}

func TestCriticVerifierMapsAcceptAndSetsState(t *testing.T) {
	sc := &stubCritic{verdict: critic.Verdict{Accept: true}}
	v := NewCriticVerifier(sc)
	tk := &Task{
		Plan:    &gantry.Plan{Steps: []gantry.PlanStep{{Description: "d", AcceptanceCriteria: "c"}}},
		Working: []gantry.Message{{Role: gantry.RoleAssistant, Content: "final answer"}},
	}

	ok, reason := v.Verify(context.Background(), tk)
	if !ok || reason != "" {
		t.Errorf("Verify = (%v, %q), want (true, \"\")", ok, reason)
	}
	if sc.gotPlan != tk.Plan {
		t.Errorf("critic did not receive the task plan")
	}
	if sc.gotLast == nil || sc.gotLast.Content != "final answer" {
		t.Errorf("critic LastResponse = %+v, want last assistant content", sc.gotLast)
	}
}

func TestCriticVerifierMapsReject(t *testing.T) {
	sc := &stubCritic{verdict: critic.Verdict{Accept: false, Reason: "missing tests"}}
	v := NewCriticVerifier(sc)
	tk := &Task{Working: []gantry.Message{{Role: gantry.RoleAssistant, Content: "x"}}}

	ok, reason := v.Verify(context.Background(), tk)
	if ok {
		t.Errorf("ok = true, want false")
	}
	if reason != "missing tests" {
		t.Errorf("reason = %q, want %q", reason, "missing tests")
	}
}

func TestCriticVerifierErrorIsSoftReject(t *testing.T) {
	sc := &stubCritic{err: errors.New("boom")}
	v := NewCriticVerifier(sc)
	tk := &Task{Working: []gantry.Message{{Role: gantry.RoleAssistant, Content: "x"}}}

	ok, reason := v.Verify(context.Background(), tk)
	if ok {
		t.Errorf("critic error must be a reject, got ok=true")
	}
	if reason == "" {
		t.Errorf("expected a diagnostic reason carrying the error")
	}
}

func TestCriticVerifierNoFinalAnswerRejects(t *testing.T) {
	// No assistant message at all: Verify must reject WITHOUT consulting the
	// critic (the old path handed the critic a nil LastResponse, which
	// LLMCritic auto-passes — marking a task done with no answer).
	sc := &stubCritic{verdict: critic.Verdict{Accept: true}} // would accept if consulted
	v := NewCriticVerifier(sc)
	tk := &Task{Working: []gantry.Message{{Role: gantry.RoleUser, Content: "do it"}}}

	ok, reason := v.Verify(context.Background(), tk)
	if ok {
		t.Errorf("ok = true, want false when no assistant answer exists")
	}
	if reason != "no final answer produced" {
		t.Errorf("reason = %q, want %q", reason, "no final answer produced")
	}
	if sc.called {
		t.Errorf("critic was consulted despite there being no final answer")
	}
}

func TestCriticVerifierEmptyAssistantContentRejects(t *testing.T) {
	// A tool-call-only assistant turn (empty Content) is not a final answer.
	sc := &stubCritic{verdict: critic.Verdict{Accept: true}}
	v := NewCriticVerifier(sc)
	tk := &Task{Working: []gantry.Message{
		{Role: gantry.RoleUser, Content: "do it"},
		{Role: gantry.RoleAssistant, Content: "", ToolCalls: []gantry.ToolCall{{ID: "c1", Name: "ask_user"}}},
	}}

	ok, reason := v.Verify(context.Background(), tk)
	if ok || reason != "no final answer produced" {
		t.Errorf("Verify = (%v, %q), want (false, \"no final answer produced\")", ok, reason)
	}
	if sc.called {
		t.Errorf("critic was consulted despite there being no final answer")
	}
}

func TestCriticVerifierPrunesOwnFeedbackFromView(t *testing.T) {
	// Prior critic rejections must not reach the critic again: feeding its own
	// verdicts back snowballs the verification context run after run.
	sc := &stubCritic{verdict: critic.Verdict{Accept: false, Reason: "still no"}}
	v := NewCriticVerifier(sc)
	tk := &Task{Working: []gantry.Message{
		{Role: gantry.RoleUser, Content: "do it"},
		{Role: gantry.RoleAssistant, Content: "attempt 1"},
		{Role: gantry.RoleUser, Name: CriticAuthor, Content: "Completion rejected: attempt 1 was bad"},
		{Role: gantry.RoleAssistant, Content: "attempt 2"},
	}}

	if _, _ = v.Verify(context.Background(), tk); !sc.called {
		t.Fatalf("critic not consulted despite a final answer existing")
	}
	if len(sc.gotMsgs) != 3 {
		t.Errorf("critic saw %d messages, want 3 (critic feedback pruned): %+v", len(sc.gotMsgs), sc.gotMsgs)
	}
	for _, m := range sc.gotMsgs {
		if m.Name == CriticAuthor {
			t.Errorf("critic-authored message leaked into the critic's view: %+v", m)
		}
	}
	if sc.gotLast == nil || sc.gotLast.Content != "attempt 2" {
		t.Errorf("LastResponse = %+v, want the latest assistant content", sc.gotLast)
	}
}

func TestCriticVerifierIsVerifier(t *testing.T) {
	var _ Verifier = NewCriticVerifier(&stubCritic{})
}

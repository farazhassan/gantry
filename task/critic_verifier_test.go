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
	gotPlan *gantry.Plan
	gotLast *gantry.LLMResponse
}

func (s *stubCritic) Critique(_ context.Context, st *gantry.State) (critic.Verdict, error) {
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

func TestCriticVerifierIsVerifier(t *testing.T) {
	var _ Verifier = NewCriticVerifier(&stubCritic{})
}

package task

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
)

// scriptedReplanner is a fake Replanner recording every invocation. It returns
// the configured plan (or error) on each call.
type scriptedReplanner struct {
	calls   int
	reasons []string
	plan    *gantry.Plan
	err     error
}

func (r *scriptedReplanner) Replan(_ context.Context, _ *Task, reason string) (*gantry.Plan, error) {
	r.calls++
	r.reasons = append(r.reasons, reason)
	if r.err != nil {
		return nil, r.err
	}
	return r.plan, nil
}

func TestWithReplannerSetsSeam(t *testing.T) {
	rp := &scriptedReplanner{}
	d := NewDriver(&scriptedRunner{}, NewInMemory(), WithReplanner(rp))
	if d.replanner != rp {
		t.Errorf("d.replanner = %v, want the injected replanner", d.replanner)
	}
}

func TestWithReplannerNilKeepsDefault(t *testing.T) {
	d := NewDriver(&scriptedRunner{}, NewInMemory(), WithReplanner(nil))
	if d.replanner != nil {
		t.Errorf("d.replanner = %v, want nil (no replanning by default)", d.replanner)
	}
}

// failStep marks the hydrated plan's step idx failed with output, then ends
// the run as a max-iterations continuation so the driver keeps looping.
func failStep(idx int, output string) func(*gantry.State) *gantry.State {
	return func(in *gantry.State) *gantry.State {
		in.Plan.Steps[idx].Status = gantry.StepFailed
		in.Plan.Steps[idx].Output = output
		in.Done = true
		in.DoneReason = gantry.DoneMaxIterations
		in.Usage = gantry.Usage{InputTokens: 1, OutputTokens: 1}
		return in
	}
}

func TestAdvanceSecondRejectionTriggersReplan(t *testing.T) {
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneNoToolCalls, twoStepPlan()), // rejected #1 → hint only
		done(gantry.DoneNoToolCalls, nil),           // rejected #2 → replan
		done(gantry.DoneNoToolCalls, nil),           // accepted
	}}
	v := &flakyVerifier{passOnCall: 2} // reject calls 0 and 1, pass call 2
	rp := &scriptedReplanner{plan: &gantry.Plan{Steps: []gantry.PlanStep{
		{Description: "gather the missing evidence"},
	}}}
	d := NewDriver(runner, NewInMemory(), WithVerifier(v), WithReplanner(rp))
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskDone {
		t.Fatalf("status = %q, want done", got.Status)
	}
	if rp.calls != 1 {
		t.Fatalf("replanner called %d times, want 1 (only on the 2nd consecutive rejection)", rp.calls)
	}
	if !strings.Contains(rp.reasons[0], "not yet") {
		t.Errorf("replan reason = %q, want the verifier's rejection reason", rp.reasons[0])
	}
	if n := len(got.Plan.Steps); n != 3 {
		t.Fatalf("ledger has %d steps, want 3 (2 original + 1 appended)", n)
	}
	appended := got.Plan.Steps[2]
	if appended.Description != "gather the missing evidence" || appended.ID != "s3" {
		t.Errorf("appended step = %+v, want {ID: s3, Description: gather the missing evidence}", appended)
	}
	var noted bool
	for _, m := range got.Working {
		if m.Role == gantry.RoleUser && m.Name == CriticAuthor && strings.Contains(m.Content, "Plan revised") {
			noted = true
		}
	}
	if !noted {
		t.Errorf("no critic-authored 'Plan revised' note in Working: %+v", got.Working)
	}
	if got.ConsecutiveRejections != 0 {
		t.Errorf("ConsecutiveRejections = %d, want 0 (reset by the replan)", got.ConsecutiveRejections)
	}
}

func TestAdvanceFirstRejectionDoesNotReplan(t *testing.T) {
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneNoToolCalls, twoStepPlan()),
		done(gantry.DoneNoToolCalls, nil),
	}}
	v := &flakyVerifier{passOnCall: 1} // reject once, then pass
	rp := &scriptedReplanner{plan: &gantry.Plan{Steps: []gantry.PlanStep{{Description: "unused"}}}}
	d := NewDriver(runner, NewInMemory(), WithVerifier(v), WithReplanner(rp))
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskDone {
		t.Fatalf("status = %q, want done", got.Status)
	}
	if rp.calls != 0 {
		t.Errorf("replanner called %d times, want 0 (a single rejection only injects the critique hint)", rp.calls)
	}
	if n := len(got.Plan.Steps); n != 2 {
		t.Errorf("ledger has %d steps, want the untouched 2", n)
	}
}

func TestAdvanceStepFailedTriggersReplan(t *testing.T) {
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneMaxIterations, twoStepPlan()), // run 1: plan adopted (s1 active, s2 pending)
		failStep(0, "build broke"),                    // run 2: s1 newly fails mid-continuation
		done(gantry.DoneNoToolCalls, nil),             // run 3: finishes; default NoopVerifier accepts
	}}
	rp := &scriptedReplanner{plan: &gantry.Plan{Steps: []gantry.PlanStep{
		{Description: "fix the build"},
	}}}
	d := NewDriver(runner, NewInMemory(), WithReplanner(rp))
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskDone {
		t.Fatalf("status = %q, want done", got.Status)
	}
	if rp.calls != 1 {
		t.Fatalf("replanner called %d times, want 1 (on the newly failed step)", rp.calls)
	}
	if !strings.Contains(rp.reasons[0], "design") || !strings.Contains(rp.reasons[0], "build broke") {
		t.Errorf("replan reason = %q, want the failed step's description and output", rp.reasons[0])
	}
	if n := len(got.Plan.Steps); n != 3 {
		t.Fatalf("ledger has %d steps, want 3", n)
	}
	if got.Plan.Steps[2].ID != "s3" || got.Plan.Steps[2].Description != "fix the build" {
		t.Errorf("appended step = %+v", got.Plan.Steps[2])
	}
	if got.Plan.Steps[0].Status != gantry.StepFailed {
		t.Errorf("failed step status = %q, want failed preserved (replan appends, never rewrites)", got.Plan.Steps[0].Status)
	}
}

func TestAdvanceAlreadyFailedStepDoesNotRetriggerReplan(t *testing.T) {
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneMaxIterations, twoStepPlan()), // run 1: adopt
		failStep(0, "boom"),                           // run 2: s1 newly fails → replan #1
		done(gantry.DoneMaxIterations, nil),           // run 3: s1 STILL failed; must not re-trigger
		done(gantry.DoneNoToolCalls, nil),             // run 4: done
	}}
	rp := &scriptedReplanner{plan: &gantry.Plan{Steps: []gantry.PlanStep{{Description: "recover"}}}}
	d := NewDriver(runner, NewInMemory(), WithReplanner(rp))
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskDone {
		t.Fatalf("status = %q, want done", got.Status)
	}
	if rp.calls != 1 {
		t.Errorf("replanner called %d times, want exactly 1 (already-failed steps never re-trigger)", rp.calls)
	}
	if n := len(got.Plan.Steps); n != 3 {
		t.Errorf("ledger has %d steps, want 3 (a re-trigger would have appended more)", n)
	}
}

func TestAdvanceReplanErrorDegradesToHintOnly(t *testing.T) {
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneNoToolCalls, twoStepPlan()),
		done(gantry.DoneNoToolCalls, nil),
		done(gantry.DoneNoToolCalls, nil),
	}}
	v := &flakyVerifier{passOnCall: 999} // always reject
	rp := &scriptedReplanner{err: errors.New("replanner exploded")}
	d := NewDriver(runner, NewInMemory(), WithVerifier(v), WithReplanner(rp))
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("a replanner error must never fail Advance: %v", err)
	}
	if got.Status != TaskAwaitingInput {
		t.Errorf("status = %q, want awaiting_input (rejection cap parks, exactly as without a replanner)", got.Status)
	}
	if rp.calls != 1 {
		t.Errorf("replanner called %d times, want 1 (attempted at rejection #2; the cap parks before #3 retries)", rp.calls)
	}
	for _, m := range got.Working {
		if strings.Contains(m.Content, "Plan revised") {
			t.Errorf("found a 'Plan revised' note after a failed replan: %q", m.Content)
		}
	}
	if runner.calls != 3 {
		t.Errorf("runner called %d times, want 3 (identical to the no-replanner cap behavior)", runner.calls)
	}
}

func TestAdvanceReplanNilPlanDegrades(t *testing.T) {
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneNoToolCalls, twoStepPlan()),
		done(gantry.DoneNoToolCalls, nil),
		done(gantry.DoneNoToolCalls, nil),
	}}
	v := &flakyVerifier{passOnCall: 999} // always reject
	rp := &scriptedReplanner{plan: nil}  // Replan returns (nil, nil): nothing to adopt
	d := NewDriver(runner, NewInMemory(), WithVerifier(v), WithReplanner(rp))
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskAwaitingInput {
		t.Errorf("status = %q, want awaiting_input (empty revision degrades like an error)", got.Status)
	}
	if rp.calls != 1 {
		t.Errorf("replanner called %d times, want 1", rp.calls)
	}
	if n := len(got.Plan.Steps); n != 2 {
		t.Errorf("ledger has %d steps, want the untouched 2", n)
	}
}

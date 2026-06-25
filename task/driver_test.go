package task

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
)

// scriptedRunner is a fake Runner that returns a queued sequence of terminal
// states, one per Resume call. It records how many times it was called so tests
// can assert run counts. Each scripted state is applied on top of the State the
// driver seeded, so the test controls the outcome (DoneReason, Plan, Usage,
// PendingToolCalls) while the driver controls the inputs (Messages, hydrated
// Plan).
type scriptedRunner struct {
	steps []func(in *gantry.State) *gantry.State
	calls int
	err   error // if non-nil, returned on the call indexed by errOn
	errOn int
}

func (r *scriptedRunner) Resume(_ context.Context, in *gantry.State) (*gantry.State, error) {
	i := r.calls
	r.calls++
	if r.err != nil && i == r.errOn {
		return in, r.err
	}
	if i >= len(r.steps) {
		in.Done = true
		in.DoneReason = gantry.DoneNoToolCalls
		return in, nil
	}
	return r.steps[i](in), nil
}

func done(reason gantry.DoneReason, plan *gantry.Plan) func(*gantry.State) *gantry.State {
	return func(in *gantry.State) *gantry.State {
		in.Done = true
		in.DoneReason = reason
		if plan != nil {
			in.Plan = plan
		}
		in.Usage = gantry.Usage{InputTokens: 1, OutputTokens: 1}
		return in
	}
}

func twoStepPlan() *gantry.Plan {
	return &gantry.Plan{Goal: "g", Steps: []gantry.PlanStep{
		{Description: "design", Status: gantry.StepActive},
		{Description: "build", Status: gantry.StepPending},
	}}
}

func TestAdvanceCompleteSingleRun(t *testing.T) {
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneNoToolCalls, twoStepPlan()),
	}}
	d := NewDriver(runner, NewInMemory())
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskDone {
		t.Errorf("status = %q, want done", got.Status)
	}
	if got.Plan == nil || got.Plan.Steps[0].ID != "s1" || got.Plan.Steps[1].ID != "s2" {
		t.Errorf("plan not adopted with IDs: %+v", got.Plan)
	}
	if got.Budget.UsedRuns != 1 {
		t.Errorf("UsedRuns = %d, want 1", got.Budget.UsedRuns)
	}
	if runner.calls != 1 {
		t.Errorf("runner called %d times, want 1", runner.calls)
	}
}

func TestAdvanceLongRunningContinuation(t *testing.T) {
	plan1 := twoStepPlan()
	plan2 := func(in *gantry.State) *gantry.State {
		in.Plan.Steps[0].Status = gantry.StepDone
		in.Plan.Steps[0].Output = "designed"
		in.Done = true
		in.DoneReason = gantry.DoneMaxIterations
		in.Usage = gantry.Usage{InputTokens: 1, OutputTokens: 1}
		return in
	}
	plan3 := func(in *gantry.State) *gantry.State {
		in.Plan.Steps[1].Status = gantry.StepDone
		in.Done = true
		in.DoneReason = gantry.DoneNoToolCalls
		in.Usage = gantry.Usage{InputTokens: 1, OutputTokens: 1}
		return in
	}
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneMaxIterations, plan1),
		plan2,
		plan3,
	}}
	d := NewDriver(runner, NewInMemory())
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskDone {
		t.Errorf("status = %q, want done", got.Status)
	}
	if runner.calls != 3 {
		t.Errorf("runner called %d times, want 3", runner.calls)
	}
	if got.Plan.Steps[0].Status != gantry.StepDone || got.Plan.Steps[0].Output != "designed" {
		t.Errorf("run-1 progress lost across continuation: %+v", got.Plan.Steps[0])
	}
	if got.Plan.Steps[1].Status != gantry.StepDone {
		t.Errorf("run-3 progress missing: %+v", got.Plan.Steps[1])
	}
	if got.Budget.UsedRuns != 3 {
		t.Errorf("UsedRuns = %d, want 3", got.Budget.UsedRuns)
	}
}

func TestAdvanceSuspendAndResume(t *testing.T) {
	store := NewInMemory()
	suspend := func(in *gantry.State) *gantry.State {
		in.Done = true
		in.DoneReason = gantry.DoneClientToolCall
		in.PendingToolCalls = []gantry.ToolCall{{ID: "q1", Name: "ask_user"}}
		in.Usage = gantry.Usage{InputTokens: 1, OutputTokens: 1}
		return in
	}
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		suspend,
		done(gantry.DoneNoToolCalls, nil),
	}}
	d := NewDriver(runner, store)
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance #1: %v", err)
	}
	if got.Status != TaskAwaitingInput {
		t.Fatalf("status = %q, want awaiting_input", got.Status)
	}
	if len(got.Pending) != 1 || got.Pending[0].ID != "q1" {
		t.Fatalf("Pending = %+v, want one call q1", got.Pending)
	}
	loaded, err := store.LoadTask(context.Background(), "tk-1")
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if loaded.Status != TaskAwaitingInput || len(loaded.Pending) != 1 {
		t.Errorf("suspension not persisted: status=%q pending=%+v", loaded.Status, loaded.Pending)
	}

	got, err = d.Advance(context.Background(), got, "Ada")
	if err != nil {
		t.Fatalf("Advance #2: %v", err)
	}
	if got.Status != TaskDone {
		t.Errorf("status = %q, want done", got.Status)
	}
	if len(got.Pending) != 0 {
		t.Errorf("Pending not cleared after resume: %+v", got.Pending)
	}
	var foundToolResult bool
	for _, m := range got.Working {
		if m.Role == gantry.RoleTool && m.ToolCallID == "q1" && m.Content == "Ada" {
			foundToolResult = true
		}
	}
	if !foundToolResult {
		t.Errorf("answer not recorded as a tool result for q1: %+v", got.Working)
	}
}

func TestAdvanceBudgetExhaustion(t *testing.T) {
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneMaxIterations, twoStepPlan()),
		done(gantry.DoneMaxIterations, nil),
		done(gantry.DoneMaxIterations, nil),
	}}
	d := NewDriver(runner, NewInMemory())
	tk := &Task{ID: "tk-1", Status: TaskPending, Budget: TaskBudget{MaxRuns: 2}}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskFailed {
		t.Errorf("status = %q, want failed", got.Status)
	}
	if got.Budget.UsedRuns != 2 {
		t.Errorf("UsedRuns = %d, want exactly 2", got.Budget.UsedRuns)
	}
	if runner.calls != 2 {
		t.Errorf("runner called %d times, want 2 (budget must stop the 3rd)", runner.calls)
	}
}

func TestAdvanceVerifierRejectThenPass(t *testing.T) {
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneNoToolCalls, twoStepPlan()),
		done(gantry.DoneNoToolCalls, nil),
	}}
	v := &flakyVerifier{passOnCall: 1}
	d := NewDriver(runner, NewInMemory(), WithVerifier(v))
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskDone {
		t.Errorf("status = %q, want done", got.Status)
	}
	if runner.calls != 2 {
		t.Errorf("runner called %d times, want 2 (reject then pass)", runner.calls)
	}
	if v.calls != 2 {
		t.Errorf("verifier called %d times, want 2", v.calls)
	}
}

func TestAdvanceRejectInjectsCriticFeedback(t *testing.T) {
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneNoToolCalls, twoStepPlan()),
		done(gantry.DoneNoToolCalls, nil),
	}}
	v := &flakyVerifier{passOnCall: 1} // reject once, then pass
	d := NewDriver(runner, NewInMemory(), WithVerifier(v))
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskDone {
		t.Fatalf("status = %q, want done", got.Status)
	}
	var found gantry.Message
	for _, m := range got.Working {
		if m.Role == gantry.RoleSystem && m.Name == CriticAuthor {
			found = m
		}
	}
	if found.Content == "" {
		t.Fatalf("no critic feedback message injected into Working: %+v", got.Working)
	}
	if !strings.Contains(found.Content, "not yet") {
		t.Errorf("feedback missing the rejection reason; got %q", found.Content)
	}
	if got.ConsecutiveRejections != 0 {
		t.Errorf("ConsecutiveRejections = %d, want 0 after a successful done", got.ConsecutiveRejections)
	}
}

func TestAdvanceRepeatedRejectionCapFails(t *testing.T) {
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneNoToolCalls, twoStepPlan()),
		done(gantry.DoneNoToolCalls, nil),
		done(gantry.DoneNoToolCalls, nil),
		done(gantry.DoneNoToolCalls, nil),
	}}
	v := &flakyVerifier{passOnCall: 999} // always reject
	d := NewDriver(runner, NewInMemory(), WithVerifier(v))
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskFailed {
		t.Errorf("status = %q, want failed after repeated rejections", got.Status)
	}
	if got.ConsecutiveRejections != 3 {
		t.Errorf("ConsecutiveRejections = %d, want 3 (the cap)", got.ConsecutiveRejections)
	}
	if runner.calls != 3 {
		t.Errorf("runner called %d times, want 3 (cap stops the 4th)", runner.calls)
	}
}

type flakyVerifier struct {
	calls      int
	passOnCall int
}

func (v *flakyVerifier) Verify(context.Context, *Task) (bool, string) {
	i := v.calls
	v.calls++
	if i >= v.passOnCall {
		return true, ""
	}
	return false, "not yet"
}

func TestAdvanceErrorTerminalFails(t *testing.T) {
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneGuardrailBlocked, nil),
	}}
	d := NewDriver(runner, NewInMemory())
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("guardrail terminal must not be a Go error: %v", err)
	}
	if got.Status != TaskFailed {
		t.Errorf("status = %q, want failed", got.Status)
	}
}

func TestAdvanceRunnerErrorWraps(t *testing.T) {
	sentinel := errors.New("llm exploded")
	runner := &scriptedRunner{
		steps: []func(*gantry.State) *gantry.State{done(gantry.DoneNoToolCalls, nil)},
		err:   sentinel,
		errOn: 0,
	}
	d := NewDriver(runner, NewInMemory())
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapped sentinel", err)
	}
	if got.Status != TaskFailed {
		t.Errorf("status = %q, want failed", got.Status)
	}
}

func TestAdvancePlanlessTaskCompletes(t *testing.T) {
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneNoToolCalls, nil),
	}}
	d := NewDriver(runner, NewInMemory())
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "quick question")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskDone {
		t.Errorf("status = %q, want done", got.Status)
	}
	if got.Plan != nil {
		t.Errorf("planless task must have no plan, got %+v", got.Plan)
	}
}

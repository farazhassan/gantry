package task

import (
	"context"
	"errors"
	"fmt"
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
		if m.Name == CriticAuthor {
			found = m
		}
	}
	if found.Content == "" {
		t.Fatalf("no critic feedback message injected into Working: %+v", got.Working)
	}
	if found.Role != gantry.RoleUser {
		t.Errorf("critic feedback role = %q, want user (adapters have no mid-transcript system slot)", found.Role)
	}
	if !strings.Contains(found.Content, "not yet") {
		t.Errorf("feedback missing the rejection reason; got %q", found.Content)
	}
	if got.ConsecutiveRejections != 0 {
		t.Errorf("ConsecutiveRejections = %d, want 0 after a successful done", got.ConsecutiveRejections)
	}
}

func TestAdvanceRepeatedRejectionCapSuspendsForInput(t *testing.T) {
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
	if got.Status != TaskAwaitingInput {
		t.Errorf("status = %q, want awaiting_input after repeated rejections (park for a human reply instead of failing)", got.Status)
	}
	if got.ConsecutiveRejections != 0 {
		t.Errorf("ConsecutiveRejections = %d, want 0 (reset on suspend, like a real ask_user suspension)", got.ConsecutiveRejections)
	}
	if len(got.Pending) != 0 {
		t.Errorf("Pending = %+v, want empty (no tool call to fulfill on resume)", got.Pending)
	}
	if runner.calls != 3 {
		t.Errorf("runner called %d times, want 3 (cap stops the 4th)", runner.calls)
	}

	// Resume: the human's reply should append as a plain user turn (no
	// ToolCallID), not a tool-result message, since nothing was pending.
	runner.steps = append(runner.steps, done(gantry.DoneNoToolCalls, nil))
	v.passOnCall = 0 // pass on this next call
	got, err = d.Advance(context.Background(), got, "here are the keywords you asked for")
	if err != nil {
		t.Fatalf("Advance (resume): %v", err)
	}
	if got.Status != TaskDone {
		t.Errorf("status after resume = %q, want done", got.Status)
	}
	var resumed gantry.Message
	found := false
	for _, m := range got.Working {
		if m.Content == "here are the keywords you asked for" {
			resumed = m
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("resume message not found in Working: %+v", got.Working)
	}
	if resumed.Role != gantry.RoleUser {
		t.Errorf("resumed message role = %q, want user; ToolCallID = %q", resumed.Role, resumed.ToolCallID)
	}
}

// TestAdvanceOscillatingRejectionCapSuspends covers a model that alternates a
// rejected done attempt with a max-iteration continuation. Each continuation
// resets ConsecutiveRejections, so the consecutive cap never fires; the
// TotalRejections cap is the backstop that stops the spin (instead of leaving it
// to the budget) by suspending the task for a human reply.
func TestAdvanceOscillatingRejectionCapSuspends(t *testing.T) {
	// reject, continue, reject, continue, ... until the total-rejection cap fires.
	var steps []func(*gantry.State) *gantry.State
	for i := 0; i < maxTotalRejections; i++ {
		steps = append(steps,
			done(gantry.DoneNoToolCalls, nil),   // rejected done attempt
			done(gantry.DoneMaxIterations, nil), // continuation resets the streak
		)
	}
	steps[0] = done(gantry.DoneNoToolCalls, twoStepPlan()) // seed a plan on the first run
	runner := &scriptedRunner{steps: steps}
	v := &flakyVerifier{passOnCall: 999} // always reject
	d := NewDriver(runner, NewInMemory(), WithVerifier(v))
	tk := &Task{ID: "tk-1", Status: TaskPending} // unlimited budget — only the cap can stop it

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskAwaitingInput {
		t.Errorf("status = %q, want awaiting_input via the total-rejection cap", got.Status)
	}
	if got.TotalRejections != maxTotalRejections {
		t.Errorf("TotalRejections = %d, want %d (the total cap)", got.TotalRejections, maxTotalRejections)
	}
	if got.ConsecutiveRejections != 0 {
		t.Errorf("ConsecutiveRejections = %d, want 0 (reset on suspend)", got.ConsecutiveRejections)
	}
	// One reject + one continuation per cycle, minus the trailing continuation that
	// never runs because the final reject trips the cap.
	wantCalls := maxTotalRejections*2 - 1
	if runner.calls != wantCalls {
		t.Errorf("runner called %d times, want %d", runner.calls, wantCalls)
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

func TestAdvanceContextCanceledCancels(t *testing.T) {
	// A run interrupted by context cancellation is a clean TaskCancelled terminal,
	// not a Go error and not TaskFailed.
	runner := &scriptedRunner{
		steps: []func(*gantry.State) *gantry.State{done(gantry.DoneNoToolCalls, nil)},
		err:   context.Canceled,
		errOn: 0,
	}
	d := NewDriver(runner, NewInMemory())
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("canceled run must not be a Go error: %v", err)
	}
	if got.Status != TaskCancelled {
		t.Errorf("status = %q, want cancelled", got.Status)
	}
}

func TestAdvanceDeadlineExceededFails(t *testing.T) {
	// A timeout is a failure, not a user cancel: only context.Canceled maps to
	// TaskCancelled. This guards against broadening the check to ctx.Err() != nil.
	runner := &scriptedRunner{
		steps: []func(*gantry.State) *gantry.State{done(gantry.DoneNoToolCalls, nil)},
		err:   context.DeadlineExceeded,
		errOn: 0,
	}
	d := NewDriver(runner, NewInMemory())
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err == nil {
		t.Fatalf("deadline-exceeded run must return a Go error")
	}
	if got.Status != TaskFailed {
		t.Errorf("status = %q, want failed", got.Status)
	}
}

func TestAdvanceSeedsTaskIdentityInMeta(t *testing.T) {
	var gotMeta map[string]any
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		func(in *gantry.State) *gantry.State {
			gotMeta = in.Meta
			in.Done = true
			in.DoneReason = gantry.DoneNoToolCalls
			return in
		},
	}}
	d := NewDriver(runner, NewInMemory())
	tk := &Task{ID: "tk-9", SessionID: "sess-9", Status: TaskPending}
	if _, err := d.Advance(context.Background(), tk, "go"); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if gotMeta[MetaTaskID] != "tk-9" || gotMeta[MetaSessionID] != "sess-9" {
		t.Fatalf("Meta = %+v, want %s=tk-9 %s=sess-9", gotMeta, MetaTaskID, MetaSessionID)
	}
}

func TestAdvanceFlushAdoptsMidRunAddedSteps(t *testing.T) {
	// Run 1 adopts a two-step skeleton, then hits its per-run cap. Run 2 mutates
	// the hydrated projection the way the update_plan interception does: marks
	// s1 done with an output and appends a new step with a minted id ("s3" —
	// len+1 over the projection, matching components/planner minting). Flush
	// via adoptOrFlush (driver.go) must round-trip BOTH the progress update and
	// the new step into the ledger; run 3 must see all three steps hydrated.
	addStep := func(in *gantry.State) *gantry.State {
		in.Plan.Steps[0].Status = gantry.StepDone
		in.Plan.Steps[0].Output = "designed"
		in.Plan.Steps = append(in.Plan.Steps, gantry.PlanStep{
			ID:                 "s3",
			Description:        "write docs",
			Status:             gantry.StepPending,
			AcceptanceCriteria: "README updated",
		})
		in.Done = true
		in.DoneReason = gantry.DoneMaxIterations
		in.Usage = gantry.Usage{InputTokens: 1, OutputTokens: 1}
		return in
	}
	var lastHydrated *gantry.Plan
	finish := func(in *gantry.State) *gantry.State {
		lastHydrated = in.Plan
		in.Done = true
		in.DoneReason = gantry.DoneNoToolCalls
		in.Usage = gantry.Usage{InputTokens: 1, OutputTokens: 1}
		return in
	}
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneMaxIterations, twoStepPlan()), // run 1: adopt skeleton (ids s1, s2)
		addStep, // run 2: progress + mid-run added step
		finish,  // run 3: capture what Hydrate produced, then complete
	}}
	d := NewDriver(runner, NewInMemory())
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskDone {
		t.Fatalf("status = %q, want done", got.Status)
	}
	if runner.calls != 3 {
		t.Errorf("runner called %d times, want 3", runner.calls)
	}
	// The ledger holds all three steps; the added one kept its minted id.
	if len(got.Plan.Steps) != 3 {
		t.Fatalf("ledger steps = %d, want 3 (added step adopted, not dropped)", len(got.Plan.Steps))
	}
	if got.Plan.Steps[0].Status != gantry.StepDone || got.Plan.Steps[0].Output != "designed" {
		t.Errorf("run-2 progress lost: %+v", got.Plan.Steps[0])
	}
	added := got.Plan.Steps[2]
	if added.ID != "s3" || added.Description != "write docs" || added.AcceptanceCriteria != "README updated" {
		t.Errorf("adopted step = %+v, want (s3, write docs, README updated)", added)
	}
	// Run 3's hydration included the adopted step — the full round trip.
	if lastHydrated == nil || len(lastHydrated.Steps) != 3 || lastHydrated.Steps[2].ID != "s3" {
		t.Errorf("run 3 hydration missing the adopted step: %+v", lastHydrated)
	}
}

func TestAdvanceHydratesBoundedOutputsAndPreservesLedger(t *testing.T) {
	longOut := strings.Repeat("x", DefaultOutputRuneBudget+100)
	var seen string
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		func(in *gantry.State) *gantry.State {
			seen = in.Plan.Steps[0].Output // what the run actually receives
			in.Done = true
			in.DoneReason = gantry.DoneNoToolCalls
			return in
		},
	}}
	d := NewDriver(runner, NewInMemory())
	tk := &Task{
		ID:     "tk-1",
		Status: TaskActive,
		Plan: &gantry.Plan{Steps: []gantry.PlanStep{
			{ID: "s1", Description: "prior", Status: gantry.StepDone, Output: longOut},
			{ID: "s2", Description: "next", Status: gantry.StepActive},
		}},
	}

	got, err := d.Advance(context.Background(), tk, "continue")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	wantSeen := strings.Repeat("x", DefaultOutputRuneBudget) +
		fmt.Sprintf("… (truncated, %d total)", DefaultOutputRuneBudget+100)
	if seen != wantSeen {
		t.Errorf("run saw output of %d runes, want the bounded projection", len([]rune(seen)))
	}
	if got.Plan.Steps[0].Output != longOut {
		t.Errorf("ledger output clobbered by the bounded projection round-trip (Flush guard failed)")
	}
}

func TestWithHydrateOutputRunesOverridesBudget(t *testing.T) {
	var seen string
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		func(in *gantry.State) *gantry.State {
			seen = in.Plan.Steps[0].Output
			in.Done = true
			in.DoneReason = gantry.DoneNoToolCalls
			return in
		},
	}}
	d := NewDriver(runner, NewInMemory(), WithHydrateOutputRunes(5))
	tk := &Task{
		ID:     "tk-1",
		Status: TaskActive,
		Plan: &gantry.Plan{Steps: []gantry.PlanStep{
			{ID: "s1", Status: gantry.StepDone, Output: "abcdefghij"},
		}},
	}
	if _, err := d.Advance(context.Background(), tk, "continue"); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if want := "abcde… (truncated, 10 total)"; seen != want {
		t.Errorf("seen = %q, want %q", seen, want)
	}
}

// ctxRespectingStore refuses writes once the caller's ctx is cancelled or
// expired, mimicking a real database-backed TaskStore (InMemoryStore ignores
// ctx, which is exactly why the bug never showed in tests).
type ctxRespectingStore struct {
	inner *InMemoryStore
}

func (s *ctxRespectingStore) SaveTask(ctx context.Context, t *Task) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.inner.SaveTask(ctx, t)
}

func (s *ctxRespectingStore) LoadTask(ctx context.Context, id string) (*Task, error) {
	return s.inner.LoadTask(ctx, id)
}

func (s *ctxRespectingStore) ListBySession(ctx context.Context, sessionID string) ([]TaskRef, error) {
	return s.inner.ListBySession(ctx, sessionID)
}

// selfCancellingRunner cancels the run's context and reports context.Canceled,
// modelling a user interrupt landing mid-run.
type selfCancellingRunner struct{ cancel context.CancelFunc }

func (r *selfCancellingRunner) Resume(_ context.Context, st *gantry.State) (*gantry.State, error) {
	r.cancel()
	return st, context.Canceled
}

func TestAdvanceCancelledStatusPersistsDespiteDeadCtx(t *testing.T) {
	store := &ctxRespectingStore{inner: NewInMemory()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := NewDriver(&selfCancellingRunner{cancel: cancel}, store)
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(ctx, tk, "do it")
	if err != nil {
		t.Fatalf("cancelled run must not be a Go error: %v", err)
	}
	if got.Status != TaskCancelled {
		t.Fatalf("status = %q, want cancelled", got.Status)
	}
	loaded, err := store.LoadTask(context.Background(), "tk-1")
	if err != nil {
		t.Fatalf("TaskCancelled was not persisted (save used the dead ctx): %v", err)
	}
	if loaded.Status != TaskCancelled {
		t.Errorf("persisted status = %q, want cancelled", loaded.Status)
	}
}

// cancelThenFailRunner kills the ctx then reports an ordinary runner error, so
// the TaskFailed best-effort save also runs against a dead context.
type cancelThenFailRunner struct{ cancel context.CancelFunc }

func (r *cancelThenFailRunner) Resume(_ context.Context, st *gantry.State) (*gantry.State, error) {
	r.cancel()
	return st, errors.New("llm exploded mid-flight")
}

func TestAdvanceFailedStatusPersistsDespiteDeadCtx(t *testing.T) {
	store := &ctxRespectingStore{inner: NewInMemory()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := NewDriver(&cancelThenFailRunner{cancel: cancel}, store)
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(ctx, tk, "do it")
	if err == nil {
		t.Fatalf("runner error must surface as a Go error")
	}
	if got.Status != TaskFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	loaded, err := store.LoadTask(context.Background(), "tk-1")
	if err != nil {
		t.Fatalf("TaskFailed was not persisted (save used the dead ctx): %v", err)
	}
	if loaded.Status != TaskFailed {
		t.Errorf("persisted status = %q, want failed", loaded.Status)
	}
}

// suspendWithCalls yields awaiting-input with one parked ask_user call per id.
func suspendWithCalls(ids ...string) func(*gantry.State) *gantry.State {
	return func(in *gantry.State) *gantry.State {
		in.Done = true
		in.DoneReason = gantry.DoneClientToolCall
		for _, id := range ids {
			in.PendingToolCalls = append(in.PendingToolCalls, gantry.ToolCall{ID: id, Name: "ask_user"})
		}
		in.Usage = gantry.Usage{InputTokens: 1, OutputTokens: 1}
		return in
	}
}

// answersByCallID indexes the transcript's tool results by ToolCallID.
func answersByCallID(msgs []gantry.Message) map[string]string {
	out := map[string]string{}
	for _, m := range msgs {
		if m.Role == gantry.RoleTool {
			out[m.ToolCallID] = m.Content
		}
	}
	return out
}

func TestAdvanceWithAnswersPerCall(t *testing.T) {
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		suspendWithCalls("q1", "q2"),
		done(gantry.DoneNoToolCalls, nil),
	}}
	d := NewDriver(runner, NewInMemory())
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskAwaitingInput || len(got.Pending) != 2 {
		t.Fatalf("setup: status=%v pending=%+v", got.Status, got.Pending)
	}

	got, err = d.AdvanceWithAnswers(context.Background(), got, map[string]string{"q1": "alpha", "q2": "beta"})
	if err != nil {
		t.Fatalf("AdvanceWithAnswers: %v", err)
	}
	if got.Status != TaskDone {
		t.Errorf("status = %q, want done", got.Status)
	}
	res := answersByCallID(got.Working)
	if res["q1"] != "alpha" || res["q2"] != "beta" {
		t.Errorf("answers = %v, want q1:alpha q2:beta", res)
	}
}

func TestAdvanceWithAnswersMissingOrEmptyGetsPlaceholder(t *testing.T) {
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		suspendWithCalls("q1", "q2", "q3"),
		done(gantry.DoneNoToolCalls, nil),
	}}
	d := NewDriver(runner, NewInMemory())
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, _ := d.Advance(context.Background(), tk, "do it")
	got, err := d.AdvanceWithAnswers(context.Background(), got, map[string]string{
		"q1":    "alpha",
		"q2":    "",        // present but empty → placeholder
		"ghost": "ignored", // unknown id → ignored
	})
	if err != nil {
		t.Fatalf("AdvanceWithAnswers: %v", err)
	}
	res := answersByCallID(got.Working)
	if res["q1"] != "alpha" {
		t.Errorf("q1 = %q, want alpha", res["q1"])
	}
	if res["q2"] != NoAnswer || res["q3"] != NoAnswer {
		t.Errorf("q2/q3 = %q/%q, want the %q placeholder for both", res["q2"], res["q3"], NoAnswer)
	}
	if _, ok := res["ghost"]; ok {
		t.Errorf("unknown answer key produced a tool result")
	}
}

func TestAdvanceWithAnswersRequiresPendingCalls(t *testing.T) {
	d := NewDriver(&scriptedRunner{}, NewInMemory())
	// Fresh task: not awaiting input.
	if _, err := d.AdvanceWithAnswers(context.Background(), &Task{ID: "a", Status: TaskPending}, nil); err == nil {
		t.Errorf("fresh task: want an error")
	}
	// Rejection-cap park: awaiting input with nothing pending — Advance's plain
	// user-turn resume is the right tool there, not per-call answers.
	if _, err := d.AdvanceWithAnswers(context.Background(), &Task{ID: "b", Status: TaskAwaitingInput}, map[string]string{"q1": "x"}); err == nil {
		t.Errorf("parked task with no pending calls: want an error")
	}
}

func TestAdvanceSinglePendingDelegatesToAnswers(t *testing.T) {
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		suspendWithCalls("q1"),
		done(gantry.DoneNoToolCalls, nil),
	}}
	d := NewDriver(runner, NewInMemory())
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, _ := d.Advance(context.Background(), tk, "go")
	got, err := d.Advance(context.Background(), got, "Ada")
	if err != nil {
		t.Fatalf("Advance (resume): %v", err)
	}
	if res := answersByCallID(got.Working); res["q1"] != "Ada" {
		t.Errorf("q1 = %q, want Ada", res["q1"])
	}
}

func TestAdvanceMultiPendingBroadcastsLegacy(t *testing.T) {
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		suspendWithCalls("q1", "q2"),
		done(gantry.DoneNoToolCalls, nil),
	}}
	d := NewDriver(runner, NewInMemory())
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, _ := d.Advance(context.Background(), tk, "go")
	got, err := d.Advance(context.Background(), got, "same answer")
	if err != nil {
		t.Fatalf("Advance (resume): %v", err)
	}
	res := answersByCallID(got.Working)
	if res["q1"] != "same answer" || res["q2"] != "same answer" {
		t.Errorf("broadcast answers = %v, want both calls to receive the input", res)
	}
}

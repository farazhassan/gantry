package task

import (
	"context"
	"fmt"
	"time"

	"github.com/farazhassan/gantry"
)

// Runner is the run seam the driver depends on: run a prepared, non-terminal
// State to termination. *gantry.Agent satisfies it via Resume. Depending on this
// behavior (rather than the concrete *Agent) lets driver tests inject a scripted
// fake instead of a live LLM.
type Runner interface {
	Resume(ctx context.Context, prior *gantry.State) (*gantry.State, error)
}

// Driver executes a Task across as many bounded runs as its budget allows. It is
// a sibling to session.Manager: it owns the multi-run loop and the hydrate/flush
// boundary, leaving the core agent loop and middleware untouched.
type Driver struct {
	agent    Runner
	store    TaskStore
	verifier Verifier
}

// Option configures a Driver at construction.
type Option func(*Driver)

// WithVerifier overrides the default NoopVerifier. Phase 3 wires the critic
// through here. A nil verifier is ignored (the default is kept).
func WithVerifier(v Verifier) Option {
	return func(d *Driver) {
		if v != nil {
			d.verifier = v
		}
	}
}

// NewDriver builds a Driver over an agent (Runner) and a TaskStore. By default it
// uses NoopVerifier, so a task's first final answer is also its completion.
func NewDriver(agent Runner, store TaskStore, opts ...Option) *Driver {
	d := &Driver{agent: agent, store: store, verifier: NoopVerifier{}}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Advance drives t across as many runs as needed until it reaches a terminal
// state (done/failed), suspends (awaiting_input), or exhausts its budget. input
// seeds the first run (the request) or supplies the answer on resume. The
// returned *Task is the same pointer, mutated and persisted. The error is
// non-nil only on infrastructural failure (a runner error or a store error); a
// normal TaskFailed outcome is not an error — callers inspect t.Status.
func (d *Driver) Advance(ctx context.Context, t *Task, input string) (*Task, error) {
	if t.Status == TaskAwaitingInput {
		// Resume: fulfill the parked ask_user call(s) with the user's answer.
		for _, call := range t.Pending {
			t.Working = append(t.Working, gantry.Message{
				Role:       gantry.RoleTool,
				ToolCallID: call.ID,
				Content:    input,
			})
		}
		t.Pending = nil
		t.Status = TaskActive
	} else {
		// Fresh request: append it as a user message.
		t.Working = append(t.Working, gantry.Message{Role: gantry.RoleUser, Content: input})
	}

	for {
		if t.Budget.exceeded() {
			t.Status = TaskFailed
			if err := d.save(ctx, t); err != nil {
				return t, err
			}
			return t, nil
		}

		// ---- seed a fresh, non-terminal run ----
		// Working is authoritative: the request/answer was already appended to it
		// above, so Input is left empty. DefaultStartHandler no-ops on a non-empty
		// transcript, so seeding Input here would be dead weight (and misleading on
		// resume, where input is the answer, not a fresh request).
		state := &gantry.State{
			Messages: cloneMessages(t.Working),
			Plan:     Hydrate(t), // nil on the first run → planner builds the skeleton
			Meta:     map[string]any{},
			Trace:    gantry.NewTrace(),
		}

		state, err := d.agent.Resume(ctx, state)
		if err != nil {
			t.Status = TaskFailed
			_ = d.save(ctx, t) // best effort; the runner error is the primary failure
			return t, fmt.Errorf("task: run failed: %w", err)
		}

		// ---- flush results into the ledger ----
		adoptOrFlush(t, state.Plan)
		if t.Status == TaskPending && t.Plan != nil && len(t.Plan.Steps) > 0 {
			t.Status = TaskActive // "no active without a plan" invariant
		}
		t.Working = state.Messages
		t.Budget.recordRun(state.Usage)

		// ---- decide ----
		switch {
		case isAskSuspension(state):
			t.Status = TaskAwaitingInput
			t.Pending = state.PendingToolCalls
			if err := d.save(ctx, t); err != nil {
				return t, err
			}
			return t, nil
		case state.DoneReason == gantry.DoneNoToolCalls:
			if ok, _ := d.verifier.Verify(ctx, t); ok {
				t.Status = TaskDone
				if err := d.save(ctx, t); err != nil {
					return t, err
				}
				return t, nil
			}
			// Verifier-fail → continue from the working context (no new input).
			// Dormant under NoopVerifier (always passes), so it cannot spin today.
			// A real Phase-3 verifier that rejects while the run keeps returning
			// DoneNoToolCalls with no plan progress would loop until the budget
			// stops it; that path should grow a no-progress guard when the critic
			// lands. The next iteration re-seeds from t.Working — see above.
		case state.DoneReason == gantry.DoneMaxIterations:
			// Run hit its per-run cap mid-work; continue with another run from the
			// working context. This is the normal long-running continuation.
		default:
			t.Status = TaskFailed // budget/guardrail/human-abort/error terminals
			if err := d.save(ctx, t); err != nil {
				return t, err
			}
			return t, nil
		}

		if err := d.save(ctx, t); err != nil {
			return t, err // persist progress between runs
		}
	}
}

// isAskSuspension reports whether a run yielded because the model called a
// client tool (ask_user) and the call is unfulfilled. In Phase 2 ask_user is the
// only client tool a task uses, so any client-tool suspension is a "needs input"
// yield.
func isAskSuspension(s *gantry.State) bool {
	return s.DoneReason == gantry.DoneClientToolCall && len(s.PendingToolCalls) > 0
}

// cloneMessages returns an independent copy of the working transcript so a run
// mutates its own State, not the task's stored Working slice, mid-loop.
func cloneMessages(src []gantry.Message) []gantry.Message {
	if src == nil {
		return nil
	}
	out := make([]gantry.Message, len(src))
	copy(out, src)
	return out
}

// save stamps UpdatedAt and persists the task.
func (d *Driver) save(ctx context.Context, t *Task) error {
	t.UpdatedAt = time.Now()
	if err := d.store.SaveTask(ctx, t); err != nil {
		return fmt.Errorf("task: save failed: %w", err)
	}
	return nil
}

// Compile-time check: *gantry.Agent satisfies Runner via its Resume method.
var _ Runner = (*gantry.Agent)(nil)

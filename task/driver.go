package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/farazhassan/gantry"
)

// maxConsecutiveRejections bounds how many times the driver re-prompts after a
// verifier rejection before failing the task. The budget still caps absolute
// spend; this fails faster with a clearer cause when the model cannot satisfy
// the critic.
const maxConsecutiveRejections = 3

// maxTotalRejections bounds rejections across the task's whole life, regardless of
// max-iteration continuations resetting the consecutive streak. It backstops a
// model that oscillates between rejected done attempts and continuations: that
// pattern keeps ConsecutiveRejections at zero, so without this the only stop would
// be the budget (a vague cause). This fails with the same clear "stubborn
// rejection" cause as the consecutive cap, just over a wider window.
const maxTotalRejections = 5

// NoAnswer is the tool-result content recorded for a parked ask_user call that
// AdvanceWithAnswers received no answer for (id missing from the map, or
// mapped to the empty string). Every pending call must receive a tool result
// or the transcript would carry an unfulfilled call; the explicit placeholder
// tells the model the user declined/skipped that question.
const NoAnswer = "(no answer provided)"

// Meta keys the Driver seeds on each run's State (State.Meta) so a stateful
// Runner — one Runner drives every task — can tell which task and session the
// current run belongs to. The canonical constants live in package gantry
// (gantry.MetaTaskID / gantry.MetaSessionID) so the core run loop can read
// them without importing this package; these aliases keep task-layer callers
// source-compatible. Values are strings.
const (
	MetaTaskID    = gantry.MetaTaskID
	MetaSessionID = gantry.MetaSessionID
)

// Runner is the run seam the driver depends on: run a prepared, non-terminal
// State to termination. *gantry.Agent satisfies it via Resume. Depending on this
// behavior (rather than the concrete *Agent) lets driver tests inject a scripted
// fake instead of a live LLM.
type Runner interface {
	Resume(ctx context.Context, prior *gantry.State) (*gantry.State, error)
}

// StreamingRunner is an OPTIONAL extension of Runner, mirroring how
// gantry.StreamingLLMClient extends LLMClient: the Driver detects support via
// a type assertion, so a plain Runner keeps working unchanged. *gantry.Agent
// satisfies it via its existing ResumeStream method.
type StreamingRunner interface {
	ResumeStream(ctx context.Context, prior *gantry.State, sink gantry.EventSink) (*gantry.State, error)
}

// Driver executes a Task across as many bounded runs as its budget allows. It is
// a sibling to session.Manager: it owns the multi-run loop and the hydrate/flush
// boundary, leaving the core agent loop and middleware untouched.
type Driver struct {
	agent        Runner
	store        TaskStore
	verifier     Verifier
	tracer       gantry.Tracer      // nil ⇒ no task spans
	sink         gantry.EventSink   // nil ⇒ no streaming; see WithEventSink
	resolver     func(*Task) Runner // nil ⇒ always the constructor Runner
	replanner    Replanner          // nil ⇒ no replanning (rejection critique-hints only)
	hydrateRunes int                // per-step Output budget for the hydrated projection
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

// WithTracer wires a Tracer so Advance opens a "task" span per drive-cycle,
// parenting the agent's run/model.call spans. Pass the SAME tracer used by the
// agent (gantry.WithTracer) so the spans nest into one trace. A nil tracer
// disables task spans (the default).
func WithTracer(tr gantry.Tracer) Option {
	return func(d *Driver) { d.tracer = tr }
}

// WithEventSink wires an EventSink so Advance streams each run's whole-run
// Events (phase transitions, text deltas, tool calls/results, done) when the
// Runner also implements StreamingRunner; a plain Runner silently falls back
// to Resume. The Driver is constructed once and drives EVERY task, so the sink
// is process-global: events from all tasks interleave on it, and consumers
// demultiplex by Event.TaskID / Event.SessionID (stamped by the agent from the
// State.Meta identity the Driver already seeds — no per-event wrapping happens
// here). The sink is called synchronously on the run goroutine; wrap slow
// consumers with gantry.NewBufferedSink so a laggard cannot stall the
// Dispatcher. A nil sink is ignored (streaming stays off, the default).
func WithEventSink(sink gantry.EventSink) Option {
	return func(d *Driver) {
		if sink != nil {
			d.sink = sink
		}
	}
}

// resume runs one prepared, non-terminal State to termination on the given
// runner (already resolved per-task via runnerFor), streaming when both a sink
// is configured and that runner supports it — the same optional-capability
// type assertion the core loop uses for StreamingLLMClient.
func (d *Driver) resume(ctx context.Context, runner Runner, state *gantry.State) (*gantry.State, error) {
	if d.sink != nil {
		if sr, ok := runner.(StreamingRunner); ok {
			return sr.ResumeStream(ctx, state, d.sink)
		}
	}
	return runner.Resume(ctx, state)
}

// WithRunnerResolver wires per-task runner resolution: before each Advance
// drive-cycle the resolver is called with the task, and a non-nil answer runs
// the task instead of the constructor Runner. Returning nil — the expected
// answer for an empty or unknown AgentProfile — falls back to the constructor
// Runner, as does leaving the resolver unset. The Runner seam itself stays
// identity-free: resolution keys off the *Task, and Resume still receives only
// the State.
func WithRunnerResolver(f func(*Task) Runner) Option {
	return func(d *Driver) { d.resolver = f }
}

// runnerFor resolves the Runner that will drive t: the resolver's non-nil
// answer, or the constructor Runner when no resolver is set or it returns nil
// (empty or unknown profile).
func (d *Driver) runnerFor(t *Task) Runner {
	if d.resolver != nil {
		if r := d.resolver(t); r != nil {
			return r
		}
	}
	return d.agent
}

// WithHydrateOutputRunes overrides the per-step Output rune budget applied
// when hydrating the plan-ledger into each run (default
// DefaultOutputRuneBudget). n <= 0 disables bounding: completed steps are
// projected with their full Output.
func WithHydrateOutputRunes(n int) Option {
	return func(d *Driver) { d.hydrateRunes = n }
}

// NewDriver builds a Driver over an agent (Runner) and a TaskStore. By default it
// uses NoopVerifier, so a task's first final answer is also its completion.
func NewDriver(agent Runner, store TaskStore, opts ...Option) *Driver {
	d := &Driver{agent: agent, store: store, verifier: NoopVerifier{}, hydrateRunes: DefaultOutputRuneBudget}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Advance drives t across as many bounded runs as its budget allows until it
// reaches a terminal state (done/failed), suspends (awaiting_input), or
// exhausts its budget. input seeds the first run (the request) or supplies the
// answer on resume. When the task is awaiting input with exactly ONE pending
// ask_user call, Advance delegates to AdvanceWithAnswers, answering that call
// (an empty input therefore records the NoAnswer placeholder). With multiple
// pending calls it broadcasts input to every call — legacy behavior; prefer
// AdvanceWithAnswers for per-call answers. The returned *Task is the same
// pointer, mutated and persisted. The error is non-nil only on infrastructural
// failure (a runner error or a store error); a normal TaskFailed outcome is
// not an error — callers inspect t.Status.
func (d *Driver) Advance(ctx context.Context, t *Task, input string) (*Task, error) {
	if t.Status == TaskAwaitingInput && len(t.Pending) == 1 {
		return d.AdvanceWithAnswers(ctx, t, map[string]string{t.Pending[0].ID: input})
	}
	if t.Status == TaskAwaitingInput {
		if len(t.Pending) > 0 {
			// Multiple parked calls, one string: broadcast the same answer to
			// each (legacy single-string resume).
			for _, call := range t.Pending {
				t.Working = append(t.Working, gantry.Message{
					Role:       gantry.RoleTool,
					ToolCallID: call.ID,
					Content:    input,
				})
			}
		} else {
			// Suspended with nothing pending: the rejection cap parked the task
			// for a human reply rather than a real ask_user call. Resume as an
			// ordinary user turn.
			t.Working = append(t.Working, gantry.Message{Role: gantry.RoleUser, Content: input})
		}
		t.Pending = nil
		t.Status = TaskActive
	} else {
		// Fresh request: append it as a user message.
		t.Working = append(t.Working, gantry.Message{Role: gantry.RoleUser, Content: input})
	}
	return d.run(ctx, t)
}

// AdvanceWithAnswers resumes an awaiting_input task, answering each parked
// ask_user call individually. answers is keyed by pending tool-call ID
// (Task.Pending[i].ID). A pending call whose id is missing from answers, or
// maps to the empty string, records the NoAnswer placeholder as its result;
// keys matching no pending call are ignored. It errors — without running —
// when the task is not awaiting input or has no pending calls (a
// rejection-cap park is resumed with Advance's plain user turn instead).
func (d *Driver) AdvanceWithAnswers(ctx context.Context, t *Task, answers map[string]string) (*Task, error) {
	if t.Status != TaskAwaitingInput || len(t.Pending) == 0 {
		return t, fmt.Errorf("task: AdvanceWithAnswers requires an awaiting_input task with pending calls (status=%q, pending=%d)", t.Status, len(t.Pending))
	}
	for _, call := range t.Pending {
		content := answers[call.ID]
		if content == "" {
			content = NoAnswer
		}
		t.Working = append(t.Working, gantry.Message{
			Role:       gantry.RoleTool,
			ToolCallID: call.ID,
			Content:    content,
		})
	}
	t.Pending = nil
	t.Status = TaskActive
	return d.run(ctx, t)
}

// run executes the multi-run drive loop for a task whose Working transcript
// has already been seeded by Advance or AdvanceWithAnswers. It owns the task
// span, the budget gate, the hydrate/flush boundary, and the decide switch.
func (d *Driver) run(ctx context.Context, t *Task) (res *Task, err error) {
	if d.tracer != nil {
		var span gantry.Span
		ctx, span = d.tracer.StartSpan(ctx, "task")
		span.SetAttr(gantry.SpanKindKey, gantry.SpanKindTask)
		span.SetAttr("task.id", t.ID)
		span.SetAttr("session.id", t.SessionID)
		span.SetAttr("task.title", t.Title)
		defer func() {
			span.SetAttr("task.status", string(t.Status))
			span.SetAttr("task.runs", t.Budget.UsedRuns)
			span.End(err)
		}()
	}

	runner := d.runnerFor(t)

	for {
		if t.Budget.exceeded() {
			t.Status = TaskFailed
			if err := d.save(ctx, t); err != nil {
				return t, err
			}
			return t, nil
		}

		// Snapshot which steps were already failed BEFORE this run so the
		// replan trigger fires only on steps that newly failed during it —
		// an already-failed step must never re-trigger a replan loop.
		failedBefore := failedStepIDs(t.Plan)

		// ---- seed a fresh, non-terminal run ----
		// Working is authoritative: the request/answer was already appended to it
		// by Advance/AdvanceWithAnswers, so Input is left empty. DefaultStartHandler
		// no-ops on a non-empty transcript, so seeding Input here would be dead
		// weight (and misleading on resume, where input is the answer, not a fresh
		// request).
		state := &gantry.State{
			Messages: cloneMessages(t.Working),
			Plan:     HydrateBounded(t, d.hydrateRunes), // nil on the first run → planner builds the skeleton
			Meta:     map[string]any{MetaTaskID: t.ID, MetaSessionID: t.SessionID},
			Trace:    gantry.NewTrace(),
		}

		state, err := d.resume(ctx, runner, state)
		if err != nil {
			// The run's ctx is typically already cancelled/expired here, so the
			// terminal status must be persisted with a detached context — a
			// ctx-respecting store would otherwise refuse the very save that
			// records the outcome.
			saveCtx := context.WithoutCancel(ctx)
			if errors.Is(err, context.Canceled) {
				// Cancellation is a clean terminal, not a failure — mirrors how a
				// consumer's turn executor treats a cancelled run.
				t.Status = TaskCancelled
				_ = d.save(saveCtx, t)
				return t, nil
			}
			t.Status = TaskFailed
			_ = d.save(saveCtx, t) // best effort; the runner error is the primary failure
			return t, fmt.Errorf("task: run failed: %w", err)
		}

		// ---- flush results into the ledger ----
		adoptOrFlush(t, state.Plan)
		newlyFailed := newlyFailedSteps(t.Plan, failedBefore)
		if t.Status == TaskPending && t.Plan != nil && len(t.Plan.Steps) > 0 {
			t.Status = TaskActive // "no active without a plan" invariant
		}
		t.Working = state.Messages
		t.Budget.recordRun(state.Usage)

		// ---- decide ----
		switch {
		case isAskSuspension(state):
			t.ConsecutiveRejections = 0
			t.Status = TaskAwaitingInput
			t.Pending = state.PendingToolCalls
			if err := d.save(ctx, t); err != nil {
				return t, err
			}
			return t, nil
		case state.DoneReason == gantry.DoneNoToolCalls:
			ok, reason := d.verifier.Verify(ctx, t)
			if ok {
				t.Status = TaskDone
				t.ConsecutiveRejections = 0
				if err := d.save(ctx, t); err != nil {
					return t, err
				}
				return t, nil
			}
			// Rejected: feed the critique back as a user turn tagged CriticAuthor
			// so the model can address the unmet criteria on the next run, then
			// continue. RoleUser, not RoleSystem: providers have no mid-transcript
			// system slot, and the Anthropic adapter silently folded RoleSystem
			// into an unmarked user turn anyway — this makes the transcript say
			// what the model actually sees. The Name tag keeps it out of
			// user-facing rendering (VisibleTranscript).
			t.Working = append(t.Working, gantry.Message{
				Role:    gantry.RoleUser,
				Name:    CriticAuthor,
				Content: "Completion rejected: " + reason + "\nAddress the unmet acceptance criteria, then finish.",
			})
			t.ConsecutiveRejections++
			t.TotalRejections++
			if t.ConsecutiveRejections >= maxConsecutiveRejections || t.TotalRejections >= maxTotalRejections {
				// Stubborn rejection: the model couldn't satisfy the verifier on its
				// own, often because it needs information only a human can supply
				// (and didn't call a client tool to ask for it). Rather than fail
				// outright, park the task for a human reply — same status a real
				// ask_user suspension uses, but with nothing pending to fulfill, so
				// Advance's resume branch appends the reply as a plain user turn
				// instead of a tool result.
				t.ConsecutiveRejections = 0
				t.Status = TaskAwaitingInput
				t.Pending = nil
				if err := d.save(ctx, t); err != nil {
					return t, err
				}
				return t, nil
			}
			// Replan escalation: on the last rejection before the consecutive cap
			// would park the task, or when this run newly failed a plan step, ask
			// the Replanner to revise the ledger. replan degrades to the critique
			// hint alone on any Replanner error (a replan failure never fails the
			// task); on success it appends the new steps and resets the streak.
			if t.ConsecutiveRejections == maxConsecutiveRejections-1 || len(newlyFailed) > 0 {
				d.replan(ctx, t, reason)
			}
		case state.DoneReason == gantry.DoneMaxIterations:
			// Run hit its per-run cap mid-work; continue with another run from the
			// working context. This is the normal long-running continuation, and it
			// counts as progress, so the rejection streak resets.
			t.ConsecutiveRejections = 0
			if len(newlyFailed) > 0 {
				d.replan(ctx, t, failedStepReason(newlyFailed))
			}
		case state.DoneReason == gantry.DoneHandoff:
			// A routing middleware asked to hand this conversation to another
			// agent. Handoff is a session-layer concept — session.Session
			// resolves the target and re-runs the turn — but the driver has no
			// agent registry, so it fails fast with a NAMED cause appended to
			// Working (the ledger records why) instead of falling through to
			// the anonymous default terminal.
			cause := "task failed: handoff is not supported inside task-driven runs yet"
			if state.Handoff != nil {
				cause += " (requested target: " + state.Handoff.Target + ")"
			}
			t.Working = append(t.Working, gantry.Message{Role: gantry.RoleSystem, Content: cause})
			t.Status = TaskFailed
			if err := d.save(ctx, t); err != nil {
				return t, err
			}
			return t, nil
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

// Compile-time check: *gantry.Agent also satisfies StreamingRunner via its
// ResumeStream method, so WithEventSink streams out of the box with the
// default agent runner.
var _ StreamingRunner = (*gantry.Agent)(nil)

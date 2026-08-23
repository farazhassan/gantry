package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
)

// defaultMaxDepth caps sub-agent nesting when WithMaxDepth is not given.
// Depth 0 is a top-level run; each delegate runs its child one level deeper,
// so the default permits three levels of nesting and refuses the fourth.
const defaultMaxDepth = 3

// delegateTool wraps a child *gantry.Agent as a tool.Tool. Invoke runs the
// child inline — a fresh run seeded with the goal plus an optional context
// briefing — and returns the child's FinalOutput as the tool result.
type delegateTool struct {
	name        string
	description string
	child       *gantry.Agent
	maxDepth    int
	timeout     time.Duration  // 0 ⇒ no timeout
	passSink    bool           // false ⇒ scope the ambient EventSink away from the child
	childReg    *tool.Registry // see WithChildRegistry
}

// Option configures a delegate tool built by New.
type Option func(*delegateTool)

// WithMaxDepth caps sub-agent nesting for this delegate. A delegate invoked
// at depth >= n returns a tool error instead of running its child. n <= 0 is
// ignored (the default of 3 is kept).
func WithMaxDepth(n int) Option {
	return func(t *delegateTool) {
		if n > 0 {
			t.maxDepth = n
		}
	}
}

// WithTimeout bounds the child run's wall-clock time. When the deadline
// passes, the child run is cancelled and the delegate returns a tool error
// wrapping context.DeadlineExceeded. d <= 0 is ignored (no timeout).
//
// The timeout applies per Invoke/Resume call, not as a cumulative cap across
// a suspended child's full lifetime: Invoke and Resume each independently
// open a fresh context.WithTimeout window of duration d. A child that
// suspends and is resumed N times gets N independent full-duration windows —
// one per round — so its aggregate wall-clock budget across every round is
// N*d, not a single d-wide cap from first Invoke to final Resume. This is
// deliberate: time spent waiting on a suspended child's answer (which may be
// arbitrarily long — a human in the loop, an external system) should not
// burn down the budget for how long any one LLM-working round is allowed to
// run.
func WithTimeout(d time.Duration) Option {
	return func(t *delegateTool) {
		if d > 0 {
			t.timeout = d
		}
	}
}

// WithEventPassthrough keeps the ambient EventSink visible to the child run.
// By default the delegate scopes the sink away with gantry.WithoutSink so
// child phase/tool/done events do not interleave with the parent's stream.
// Enable passthrough once your consumer demuxes events by identity
// (RunID/Agent fields, plan 02).
func WithEventPassthrough() Option {
	return func(t *delegateTool) { t.passSink = true }
}

// WithChildRegistry supplies the *tool.Registry the child agent was wired
// with (via tool.New, tool.NewWithPolicy, or subagent.ComponentWithRegistry)
// when the child itself may suspend on a ResumableTool — including when it
// dispatches its own nested sub-agent. Required for Resume to route an
// answer down through more than one level of nesting; omit it when the
// child only ever suspends on a declared tool.Client/DynamicClient call (no
// ResumableTools of its own).
//
// Callers fulfilling a suspend that may involve this delegate should resume
// the top-level state with subagent.Resume, not tool.Resume directly, to get
// correct usage accounting — see subagent.Resume's doc comment. A bare
// tool.Resume call is still fine when usage accounting isn't a concern for
// your use case.
func WithChildRegistry(reg *tool.Registry) Option {
	return func(t *delegateTool) { t.childReg = reg }
}

// New wraps child as a tool the parent's LLM can invoke. name and description
// are advertised verbatim in the ToolDef; the input schema is fixed:
// {"goal" (required), "context" (optional briefing string)}. child must be
// non-nil; a nil child makes every Invoke return a tool error.
func New(name, description string, child *gantry.Agent, opts ...Option) tool.Tool {
	t := &delegateTool{
		name:        name,
		description: description,
		child:       child,
		maxDepth:    defaultMaxDepth,
	}
	for _, o := range opts {
		if o != nil {
			o(t)
		}
	}
	return t
}

// compile-time check: delegateTool implements tool.Tool.
var _ tool.Tool = (*delegateTool)(nil)
var _ tool.ResumableTool = (*delegateTool)(nil)

// Definition describes the delegate to the parent's LLM.
func (t *delegateTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{
		Name:        t.name,
		Description: t.description,
		Schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "goal": {"type": "string", "description": "What the sub-agent should accomplish."},
    "context": {"type": "string", "description": "Optional briefing: relevant facts, constraints, or prior findings the sub-agent needs. It sees nothing else from this conversation."}
  },
  "required": ["goal"]
}`),
	}
}

// Invoke runs the child agent inline and returns {"output": <FinalOutput>}.
// The child sees ONLY the goal and optional context — a fresh run, no parent
// transcript. Child run errors return as tool errors: dispatch surfaces them
// to the parent's model as an error tool result and the parent run continues.
//
// Run (not Resume) is the child entry point: Run mints a fresh gantry.State
// via NewState (Trace and Meta initialized) and DefaultStartHandler seeds the
// combined goal+context as the opening user message. Resume is the primitive
// for continuing an interrupted prior state and would require hand-assembling
// a *State, duplicating NewState's initialization.
func (t *delegateTool) Invoke(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Goal    string `json:"goal"`
		Context string `json:"context"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("%s: invalid input: %w", t.name, err)
	}
	if in.Goal == "" {
		return nil, fmt.Errorf("%s: goal is required", t.name)
	}
	if t.child == nil {
		return nil, errors.New(t.name + ": no child agent configured")
	}
	depth := depthFrom(ctx)
	if depth >= t.maxDepth {
		return nil, fmt.Errorf("%s: sub-agent depth limit reached (max %d)", t.name, t.maxDepth)
	}

	seed := in.Goal
	if in.Context != "" {
		seed = in.Goal + "\n\nContext:\n" + in.Context
	}

	runCtx := withDepth(ctx, depth+1)
	if parentRunID, _, ok := gantry.CurrentIdentity(ctx); ok {
		if toolCallID, ok := tool.CallIDFrom(ctx); ok {
			runCtx = gantry.WithParentLink(runCtx, gantry.ParentLink{
				RunID:      parentRunID,
				ToolCallID: toolCallID,
			})
		}
	}
	if !t.passSink {
		// Child events would otherwise flow to the ambient sink and interleave
		// with the parent's stream. Scope it away until the consumer opts in.
		runCtx = gantry.WithoutSink(runCtx)
	}
	if t.timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(runCtx, t.timeout)
		defer cancel()
	}

	st, err := t.child.Run(runCtx, seed)
	// Fold the child's consumption into the per-dispatch recorder installed by
	// Component's usage_fold middleware (absent under plain tool.FromTools).
	// Record BEFORE the error check: the Run family always returns a non-nil
	// *State, and an errored child run has still consumed tokens.
	if rec, ok := usageRecorderFrom(ctx); ok {
		rec.add(st.Usage)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: child run failed: %w", t.name, err)
	}
	return t.asResult(st)
}

// Resume continues a suspended child run using the token from a prior
// *gantry.PendingResult.Resume (the whole child *gantry.State, marshaled)
// and the answers for its pending call(s). It delegates to tool.Resume
// unconditionally, whether the child's own suspend was a direct
// (declared-client-tool) call or itself nested through the child's own
// delegate tool — tool.Resume already handles both uniformly, so no
// path/stack bookkeeping is needed here for multi-level nesting: each level
// only ever prefixes by the one segment it owns.
//
// This method only folds its own usage delta into a recorder it finds
// already installed in ctx (see usageRecorderFrom below) — it does not
// install one itself. A top-level caller resuming an application's
// suspended run should call the package-level subagent.Resume, not this
// method or tool.Resume directly, so a recorder is actually present and this
// fold has somewhere to land; see subagent.Resume's doc comment for why a
// bare tool.Resume call cannot do this.
func (t *delegateTool) Resume(ctx context.Context, resume json.RawMessage, results []gantry.ToolResult) (json.RawMessage, error) {
	var child gantry.State
	if err := json.Unmarshal(resume, &child); err != nil {
		return nil, fmt.Errorf("%s: decoding suspended child state: %w", t.name, err)
	}
	// Reinstall any dynamic client tool defs the child had advertised via
	// tool.DynamicClient before its suspend: DynamicClient's PhaseStart
	// middleware consumes and deletes SetPendingClientTools' per-run defs the
	// moment it reads them, so the just-unmarshaled child has nothing left
	// in its own Meta for tool.Resume's eventual PhaseStart pass to
	// reinstall — see CarryDynamicClientTools's doc comment.
	if err := tool.CarryDynamicClientTools(&child); err != nil {
		return nil, fmt.Errorf("%s: reinstalling child dynamic client tools: %w", t.name, err)
	}
	// child.Usage is the child's cumulative usage as of its last suspend (or,
	// for the very first Resume after one suspend, exactly what Invoke's own
	// fold already recorded). Snapshot it now so the fold below can record
	// only what THIS round adds — see the delta comment further down.
	before := child.Usage

	depth := depthFrom(ctx)
	runCtx := withDepth(ctx, depth+1)
	if !t.passSink {
		runCtx = gantry.WithoutSink(runCtx)
	}
	if t.timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(runCtx, t.timeout)
		defer cancel()
	}

	st, err := tool.Resume(runCtx, t.child, t.childReg, &child, results)
	// st.Usage is the child's CUMULATIVE usage — gantry.Agent.Resume keeps
	// accumulating onto the same State.Usage field rather than restarting it,
	// so it already includes everything folded by a prior suspend round
	// (Invoke's own rec.add, or an earlier Resume's). Component installs a
	// fresh *usageRecorder per PhaseToolExec pass and adds its total into the
	// parent's state.Usage unconditionally at the end of every pass, so
	// folding the full cumulative total here would double-count whatever a
	// prior pass already folded. Fold only the delta this round actually
	// added: st.Usage minus the cumulative total as of just before this
	// Resume call (before, captured above from the unmarshaled child state).
	// Record BEFORE the error check, matching Invoke: the Run/Resume family
	// always returns a non-nil *State, and an errored resume may still have
	// consumed tokens before failing.
	if rec, ok := usageRecorderFrom(ctx); ok {
		rec.add(gantry.Usage{
			InputTokens:  st.Usage.InputTokens - before.InputTokens,
			OutputTokens: st.Usage.OutputTokens - before.OutputTokens,
			Cost:         st.Usage.Cost - before.Cost,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("%s: child run failed: %w", t.name, err)
	}
	return t.asResult(st)
}

// asResult converts a child run's terminal (or suspended) *gantry.State into
// this delegate's own Invoke/Resume return shape: {"output":...} on a clean
// finish, or a *gantry.PendingResult carrying the whole child state if it
// suspended (again).
func (t *delegateTool) asResult(st *gantry.State) (json.RawMessage, error) {
	if st.DoneReason == gantry.DoneClientToolCall {
		marshaled, err := json.Marshal(st)
		if err != nil {
			return nil, fmt.Errorf("%s: encoding suspended child state: %w", t.name, err)
		}
		return nil, &gantry.PendingResult{Pending: st.PendingToolCalls, Resume: marshaled}
	}
	out, err := json.Marshal(map[string]string{"output": st.FinalOutput})
	if err != nil {
		return nil, fmt.Errorf("%s: encoding output: %w", t.name, err)
	}
	return out, nil
}

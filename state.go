package gantry

// State is the mutable per-run record passed through every middleware.
// It is not safe for concurrent use except where explicitly noted
// (e.g. Trace.Record).
type State struct {
	// Input
	Input string
	Task  string // current task (may be refined by Planner in Plan 2)

	// Context being assembled this turn
	System    string
	Messages  []Message
	Tools     []ToolDef
	Retrieved []Document
	Plan      *Plan

	// Loop state
	Iteration        int
	LastResponse     *LLMResponse
	PendingToolCalls []ToolCall
	ToolResults      []ToolResult

	// Termination
	Done        bool
	DoneReason  DoneReason
	FinalOutput string
	Handoff     *Handoff // set by routing middleware alongside DoneHandoff; nil otherwise

	// Observability
	Trace *Trace
	Usage Usage

	// Escape hatch for middleware-to-middleware state.
	// Callers should namespace keys (e.g. "components/cache:key") to avoid collisions.
	Meta map[string]any
}

// NewState returns a State ready to feed into Agent.Run.
func NewState(input string) *State {
	return &State{
		Input: input,
		Trace: NewTrace(),
		Meta:  map[string]any{},
	}
}

// HandoffMode selects how a handoff moves work between agents.
type HandoffMode string

const (
	HandoffTransfer HandoffMode = "transfer" // conversation moves to the target agent
	HandoffDelegate HandoffMode = "delegate" // target runs, result returns to source
)

// Handoff is a routing decision recorded by middleware (e.g. a router's
// PhasePostLLM middleware) alongside Done/DoneHandoff. The core loop never
// reads it — the layer above the run does: session.Session re-runs a
// transfer turn on the target agent; delegate handling belongs to the
// subagent-delegate plan.
type Handoff struct {
	Target string // registry key of the target agent
	Mode   HandoffMode
	Reason string
}

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

	// ContextWindow is the model's maximum prompt tokens, resolved once per
	// run before PhaseStart from WithContextWindow or the client's
	// ContextWindowReporter. 0 means unknown.
	ContextWindow int
	// ContextUsage anchors the last provider-measured prompt size to the
	// transcript. See ContextUsage.
	ContextUsage ContextUsage

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

// ContextUsage anchors a provider-measured prompt size to the transcript.
// PromptTokens covers System, Tools and Messages[:MessageCount] as sent, plus
// the assistant reply that followed (its output tokens). MessageCount == 0
// means no valid measurement. Middleware that rewrites Messages other than by
// appending must reset it to the zero value.
type ContextUsage struct {
	PromptTokens int
	MessageCount int
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

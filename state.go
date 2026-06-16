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

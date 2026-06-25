package gantry

// Reserved span-attr keys form the contract between the agent and any Tracer
// that wants to render content natively (e.g. the Langfuse exporter). Generic
// tracers may ignore them; they are stored like any other attr.
const (
	// AttrInput is the trace-level input (run span) or the assembled generation
	// input (llm_call span).
	AttrInput = "gantry.input"
	// AttrOutput is the trace-level output (run span) or the model reply
	// (llm_call span).
	AttrOutput = "gantry.output"
	// AttrState is a sanitized snapshot of the run State (Trace excluded).
	AttrState = "gantry.state"
	// AttrUsage is the per-call token Usage for a generation.
	AttrUsage = "gantry.usage"
	// AttrObservationType marks how an exporter should model the span, e.g.
	// "generation" for an LLM call.
	AttrObservationType = "gantry.observation_type"
)

// ObservationGeneration is the AttrObservationType value for an LLM call.
const ObservationGeneration = "generation"

// genInput is the assembled context actually sent to the model. Unexported:
// exporters JSON-marshal the attr value rather than type-asserting on it.
type genInput struct {
	System   string    `json:"system,omitempty"`
	Messages []Message `json:"messages,omitempty"`
	Tools    []string  `json:"tools,omitempty"` // tool name refs only
}

// genOutput is the model reply for a generation.
type genOutput struct {
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// stateView is a sanitized, marshalable view of State: Trace is omitted
// (recursive — it is the tracer's own data) and Tools collapse to name refs
// instead of full ToolDef (which carries description + JSON schema).
type stateView struct {
	Input            string         `json:"input,omitempty"`
	Task             string         `json:"task,omitempty"`
	System           string         `json:"system,omitempty"`
	Messages         []Message      `json:"messages,omitempty"`
	ToolRefs         []string       `json:"tool_refs,omitempty"`
	Retrieved        []Document     `json:"retrieved,omitempty"`
	Plan             *Plan          `json:"plan,omitempty"`
	Iteration        int            `json:"iteration"`
	LastResponse     *LLMResponse   `json:"last_response,omitempty"`
	PendingToolCalls []ToolCall     `json:"pending_tool_calls,omitempty"`
	ToolResults      []ToolResult   `json:"tool_results,omitempty"`
	Done             bool           `json:"done"`
	DoneReason       DoneReason     `json:"done_reason,omitempty"`
	FinalOutput      string         `json:"final_output,omitempty"`
	Usage            Usage          `json:"usage"`
	Meta             map[string]any `json:"meta,omitempty"`
}

// toolRefs returns the names of the given tool defs, or nil for an empty input.
func toolRefs(defs []ToolDef) []string {
	if len(defs) == 0 {
		return nil
	}
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Name
	}
	return names
}

// cloneMessages returns an independent copy of the transcript so later phases
// (e.g. appending the assistant message) cannot mutate what was captured.
func cloneMessages(msgs []Message) []Message {
	if len(msgs) == 0 {
		return nil
	}
	cp := make([]Message, len(msgs))
	copy(cp, msgs)
	return cp
}

// stateSnapshot builds a sanitized view of s for tracing. It clones Messages
// and drops Trace and full tool defs. The remaining slices and pointers are
// stored by reference, which is safe only because tracers marshal the snapshot
// eagerly in Span.End on the agent goroutine; deferring that marshal would race
// with ongoing state mutation.
func stateSnapshot(s *State) stateView {
	return stateView{
		Input:            s.Input,
		Task:             s.Task,
		System:           s.System,
		Messages:         cloneMessages(s.Messages),
		ToolRefs:         toolRefs(s.Tools),
		Retrieved:        s.Retrieved,
		Plan:             s.Plan,
		Iteration:        s.Iteration,
		LastResponse:     s.LastResponse,
		PendingToolCalls: s.PendingToolCalls,
		ToolResults:      s.ToolResults,
		Done:             s.Done,
		DoneReason:       s.DoneReason,
		FinalOutput:      s.FinalOutput,
		Usage:            s.Usage,
		Meta:             s.Meta,
	}
}

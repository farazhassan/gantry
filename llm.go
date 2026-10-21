package gantry

import "context"

// LLMClient is the single interface the agent core requires the user to supply.
// The base library ships no adapter; users wire up Anthropic / OpenAI / etc.
type LLMClient interface {
	Generate(ctx context.Context, req LLMRequest) (LLMResponse, error)
}

// ContextWindowReporter is optionally implemented by LLMClient adapters that
// can discover the model's context window (maximum prompt tokens), typically
// from the provider's models endpoint. Implementations should cache the
// result: the agent asks once per run, and one client is shared across
// concurrent runs, so ContextWindow and its cache must be safe for
// concurrent use.
type ContextWindowReporter interface {
	ContextWindow(ctx context.Context) (int, error)
}

// LLMRequest carries a normalized prompt to the LLM.
type LLMRequest struct {
	System      string
	Messages    []Message
	Tools       []ToolDef
	ToolChoice  *ToolChoice // nil means "provider default" (auto)
	Temperature float64     // 0 means "use provider default"
	MaxTokens   int         // 0 means "use provider default"
}

// ToolChoiceMode selects how the model may use the request's Tools.
type ToolChoiceMode string

const (
	ToolChoiceAuto     ToolChoiceMode = "auto"     // model decides (the provider default)
	ToolChoiceNone     ToolChoiceMode = "none"     // model must not call tools
	ToolChoiceRequired ToolChoiceMode = "required" // model must call at least one tool
	ToolChoiceTool     ToolChoiceMode = "tool"     // force one named tool; Name required
)

// ToolChoice constrains the model's tool use for one request. Adapters map it
// to the provider's native parameter; adapters whose provider cannot express a
// forced-tool request (Mode ToolChoiceRequired or ToolChoiceTool) return a
// clear error rather than silently ignoring it. Structured output is achieved
// by forcing a single tool call (Mode ToolChoiceTool) — there is deliberately
// no separate response-format field.
type ToolChoice struct {
	Mode ToolChoiceMode
	Name string // set only when Mode == ToolChoiceTool
}

// toolChoiceKey carries a per-call ToolChoice on the request context so the
// agent loop can constrain one LLM call (the max-iterations wrap-up turn) without
// threading it through handler signatures — the same mechanism temperature
// uses (withTemperature/temperatureFrom).
type toolChoiceKey struct{}

func withToolChoice(ctx context.Context, tc *ToolChoice) context.Context {
	return context.WithValue(ctx, toolChoiceKey{}, tc)
}

// ToolChoiceFrom returns the ToolChoice the agent loop set for the current LLM
// call, or nil when none was set (provider default). DefaultLLMCallHandler
// copies it into LLMRequest.ToolChoice; custom inner PhaseLLMCall handlers
// should do the same so the max-iterations wrap-up turn stays tool-less.
func ToolChoiceFrom(ctx context.Context) *ToolChoice {
	tc, _ := ctx.Value(toolChoiceKey{}).(*ToolChoice)
	return tc
}

// LLMResponse carries the LLM's reply.
//
// Construct it with keyed fields (LLMResponse{Content: ...}); the field set
// grows over time, so unkeyed composite literals are not source-compatible
// across versions.
type LLMResponse struct {
	Content    string
	ToolCalls  []ToolCall
	StopReason StopReason
	Usage      Usage
	Model      string // model that produced the reply; adapters set it (optional)
}

// StopReason describes why the LLM stopped generating.
type StopReason string

const (
	StopReasonEnd       StopReason = "end_turn"
	StopReasonToolUse   StopReason = "tool_use"
	StopReasonMaxTokens StopReason = "max_tokens"
	// StopReasonContextWindow means generation stopped because the model's
	// context window filled up (prompt + output), as opposed to the
	// requested output cap (StopReasonMaxTokens). Anthropic reports it as
	// model_context_window_exceeded. Without tool calls the loop treats it
	// like any other non-tool stop. With tool calls their input may be cut
	// off, so DefaultLLMCallHandler never runs them and returns a
	// *ContextLengthError instead (see ContextOverflowHandler).
	StopReasonContextWindow StopReason = "context_window"
)

package gantry

import "context"

// LLMClient is the single interface the agent core requires the user to supply.
// The base library ships no adapter; users wire up Anthropic / OpenAI / etc.
type LLMClient interface {
	Generate(ctx context.Context, req LLMRequest) (LLMResponse, error)
}

// LLMRequest carries a normalized prompt to the LLM.
type LLMRequest struct {
	System      string
	Messages    []Message
	Tools       []ToolDef
	Temperature float64 // 0 means "use provider default"
	MaxTokens   int     // 0 means "use provider default"
}

// LLMResponse carries the LLM's reply.
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
)

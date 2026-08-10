package openai

import (
	"encoding/json"

	"github.com/farazhassan/gantry"
)

// The structs below mirror OpenAI's /v1/chat/completions wire format. They are
// private: callers only ever see gantry types. Mapping lives here so the
// client code in openai.go stays focused on transport.

type chatRequest struct {
	Model           string         `json:"model"`
	Messages        []chatMessage  `json:"messages"`
	Tools           []chatTool     `json:"tools,omitempty"`
	ToolChoice      any            `json:"tool_choice,omitempty"`
	Temperature     float64        `json:"temperature,omitempty"`
	MaxTokens       int            `json:"max_tokens,omitempty"`
	Stream          bool           `json:"stream"`
	StreamOptions   *streamOptions `json:"stream_options,omitempty"`
	ReasoningEffort string         `json:"reasoning_effort,omitempty"`
}

// forcedTool is the object form of tool_choice that forces one named function.
type forcedTool struct {
	Type     string         `json:"type"`
	Function forcedToolName `json:"function"`
}

type forcedToolName struct {
	Name string `json:"name"`
}

// streamOptions asks the API to emit a terminal chunk carrying usage when
// streaming (otherwise usage is omitted from streamed responses).
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// wireToolCall is the request-side assistant tool call. OpenAI carries the
// arguments as a JSON-encoded string, so gantry ToolCall.Input (already raw
// JSON) is forwarded verbatim as that string.
type wireToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function wireToolCallFunc `json:"function"`
}

type wireToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// chatResponse is one /v1/chat/completions reply. For stream=false it is the
// whole reply; for stream=true it is one SSE data payload (the incremental
// content/tool-call deltas live under choices[].delta, and a terminal payload
// carries usage when stream_options.include_usage is set).
type chatResponse struct {
	Choices []choice `json:"choices"`
	Usage   *usage   `json:"usage"`
}

type choice struct {
	Message      respMessage `json:"message"`
	Delta        respMessage `json:"delta"`
	FinishReason string      `json:"finish_reason"`
}

type respMessage struct {
	Role             string         `json:"role"`
	Content          string         `json:"content"`
	ReasoningContent string         `json:"reasoning_content"`
	ToolCalls        []respToolCall `json:"tool_calls"`
}

// respToolCall is the response-side tool call. In streaming, Index identifies
// the call across chunks and Function.Arguments accumulates partial fragments.
type respToolCall struct {
	Index    int              `json:"index"`
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function wireToolCallFunc `json:"function"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// toChatRequest maps a gantry request to the OpenAI wire format. System is
// carried as a leading system-role message. reasoningEffort, when non-empty,
// is forwarded as-is via the reasoning_effort parameter used by OpenAI's
// reasoning-capable models (o1/o3/gpt-5-family: "low"/"medium"/"high") — a
// mechanical mapping, not fixed up or validated here.
func toChatRequest(model string, req gantry.LLMRequest, stream bool, reasoningEffort string) chatRequest {
	var msgs []chatMessage
	if req.System != "" {
		msgs = append(msgs, chatMessage{Role: string(gantry.RoleSystem), Content: req.System})
	}
	for _, m := range req.Messages {
		cm := chatMessage{Role: string(m.Role), Content: m.Content}
		if m.Role == gantry.RoleTool {
			cm.ToolCallID = m.ToolCallID
		}
		for _, tc := range m.ToolCalls {
			cm.ToolCalls = append(cm.ToolCalls, wireToolCall{
				ID:       tc.ID,
				Type:     "function",
				Function: wireToolCallFunc{Name: tc.Name, Arguments: string(tc.Input)},
			})
		}
		msgs = append(msgs, cm)
	}

	cr := chatRequest{
		Model:           model,
		Messages:        msgs,
		Tools:           toChatTools(req.Tools),
		ToolChoice:      toWireToolChoice(req.ToolChoice),
		Temperature:     req.Temperature,
		MaxTokens:       req.MaxTokens,
		Stream:          stream,
		ReasoningEffort: reasoningEffort,
	}
	if stream {
		cr.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	return cr
}

func toChatTools(defs []gantry.ToolDef) []chatTool {
	if len(defs) == 0 {
		return nil
	}
	out := make([]chatTool, len(defs))
	for i, d := range defs {
		out[i] = chatTool{
			Type: "function",
			Function: chatFunction{
				Name:        d.Name,
				Description: d.Description,
				Parameters:  d.Schema,
			},
		}
	}
	return out
}

// toWireToolChoice maps the gantry tool choice to OpenAI's polymorphic
// tool_choice parameter: a bare mode string ("auto", "none", "required") or
// the forced-function object. nil (and unknown future modes) return nil so
// the field is omitted and the provider default (auto) applies. The mapping
// is mechanical — a forced tool with an empty Name is forwarded as-is and
// rejected loudly by the provider (HTTP 400 via checkStatus), never fixed up
// silently.
func toWireToolChoice(tc *gantry.ToolChoice) any {
	if tc == nil {
		return nil
	}
	switch tc.Mode {
	case gantry.ToolChoiceAuto, gantry.ToolChoiceNone, gantry.ToolChoiceRequired:
		return string(tc.Mode)
	case gantry.ToolChoiceTool:
		return forcedTool{Type: "function", Function: forcedToolName{Name: tc.Name}}
	}
	return nil
}

// assembleResponse builds the gantry response from aggregated stream/non-stream
// fields. It is the single place stop-reason and tool-call mapping live.
func assembleResponse(content string, calls []respToolCall, finishReason string, u gantry.Usage) gantry.LLMResponse {
	return gantry.LLMResponse{
		Content:    content,
		ToolCalls:  toToolCalls(calls),
		StopReason: stopReason(finishReason, len(calls) > 0),
		Usage:      u,
	}
}

// toToolCalls preserves OpenAI's per-call IDs and forwards the arguments string
// as raw JSON (it is already a serialized JSON object).
func toToolCalls(calls []respToolCall) []gantry.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]gantry.ToolCall, len(calls))
	for i, c := range calls {
		out[i] = gantry.ToolCall{
			ID:    c.ID,
			Name:  c.Function.Name,
			Input: json.RawMessage(c.Function.Arguments),
		}
	}
	return out
}

func stopReason(finishReason string, hasTools bool) gantry.StopReason {
	switch {
	case finishReason == "tool_calls" || hasTools:
		return gantry.StopReasonToolUse
	case finishReason == "length":
		return gantry.StopReasonMaxTokens
	default:
		return gantry.StopReasonEnd
	}
}

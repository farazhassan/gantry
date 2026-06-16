package ollama

import (
	"encoding/json"
	"fmt"

	"github.com/farazhassan/gantry"
)

// The structs below mirror Ollama's /api/chat wire format. They are private:
// callers only ever see harness types. Mapping happens in this file so the
// client code in ollama.go stays focused on transport.

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []chatTool    `json:"tools,omitempty"`
	Stream   bool          `json:"stream"`
	Options  *chatOptions  `json:"options,omitempty"`
}

type chatOptions struct {
	Temperature float64 `json:"temperature,omitempty"`
	NumPredict  int     `json:"num_predict,omitempty"`
}

type chatMessage struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	ToolCalls []wireToolCall `json:"tool_calls,omitempty"`
	ToolName  string         `json:"tool_name,omitempty"`
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

type wireToolCall struct {
	Function wireToolCallFunc `json:"function"`
}

type wireToolCallFunc struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// chatResponse is one /api/chat reply. For stream=false it is the whole reply;
// for stream=true it is one NDJSON line (content carries the incremental delta,
// and the final line has Done=true with DoneReason and token counts).
type chatResponse struct {
	Message         respMessage `json:"message"`
	Done            bool        `json:"done"`
	DoneReason      string      `json:"done_reason"`
	PromptEvalCount int         `json:"prompt_eval_count"`
	EvalCount       int         `json:"eval_count"`
}

type respMessage struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	ToolCalls []wireToolCall `json:"tool_calls,omitempty"`
}

// toChatRequest maps a harness request to the Ollama wire format. System is
// carried as a leading system-role message (Ollama has no separate field).
func toChatRequest(model string, req gantry.LLMRequest, stream bool) chatRequest {
	var msgs []chatMessage
	if req.System != "" {
		msgs = append(msgs, chatMessage{Role: string(gantry.RoleSystem), Content: req.System})
	}
	for _, m := range req.Messages {
		cm := chatMessage{Role: string(m.Role), Content: m.Content}
		if m.Role == gantry.RoleTool {
			cm.ToolName = m.Name
		}
		for _, tc := range m.ToolCalls {
			cm.ToolCalls = append(cm.ToolCalls, wireToolCall{
				Function: wireToolCallFunc{Name: tc.Name, Arguments: tc.Input},
			})
		}
		msgs = append(msgs, cm)
	}

	return chatRequest{
		Model:    model,
		Messages: msgs,
		Tools:    toChatTools(req.Tools),
		Stream:   stream,
		Options:  toChatOptions(req),
	}
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

// toChatOptions returns nil when neither knob is set so Ollama applies its own
// defaults (0 temperature/max-tokens both mean "provider default" in harness).
func toChatOptions(req gantry.LLMRequest) *chatOptions {
	if req.Temperature == 0 && req.MaxTokens == 0 {
		return nil
	}
	return &chatOptions{Temperature: req.Temperature, NumPredict: req.MaxTokens}
}

// assembleResponse builds the harness response from aggregated stream/non-stream
// fields. It is the single place stop-reason and tool-call mapping live.
func assembleResponse(content string, calls []wireToolCall, doneReason string, promptEval, evalCount int) gantry.LLMResponse {
	return gantry.LLMResponse{
		Content:    content,
		ToolCalls:  toToolCalls(calls),
		StopReason: stopReason(doneReason, len(calls) > 0),
		Usage:      gantry.Usage{InputTokens: promptEval, OutputTokens: evalCount},
	}
}

// toToolCalls synthesizes an ID per call. Ollama omits per-call IDs, but the
// harness links a ToolResult back to its ToolCall by ID, so a stable
// index-based id ("call-0", "call-1", ...) is required.
func toToolCalls(calls []wireToolCall) []gantry.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]gantry.ToolCall, len(calls))
	for i, c := range calls {
		out[i] = gantry.ToolCall{
			ID:    fmt.Sprintf("call-%d", i),
			Name:  c.Function.Name,
			Input: c.Function.Arguments,
		}
	}
	return out
}

func stopReason(doneReason string, hasTools bool) gantry.StopReason {
	switch {
	case hasTools:
		return gantry.StopReasonToolUse
	case doneReason == "length":
		return gantry.StopReasonMaxTokens
	default:
		return gantry.StopReasonEnd
	}
}

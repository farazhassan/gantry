package anthropic

import (
	"encoding/json"
	"strings"

	"github.com/farazhassan/gantry"
)

// The structs below mirror Anthropic's /v1/messages wire format. They are
// private: callers only ever see harness types. Mapping lives here so the
// client code in anthropic.go stays focused on transport.

type chatRequest struct {
	Model       string       `json:"model"`
	System      string       `json:"system,omitempty"`
	Messages    []reqMessage `json:"messages"`
	Tools       []reqTool    `json:"tools,omitempty"`
	MaxTokens   int          `json:"max_tokens"`
	Temperature float64      `json:"temperature,omitempty"`
	Stream      bool         `json:"stream"`
}

type reqMessage struct {
	Role    string     `json:"role"`
	Content []reqBlock `json:"content"`
}

// reqBlock is one content block. The active fields depend on Type: "text" uses
// Text; "tool_use" uses ID/Name/Input; "tool_result" uses ToolUseID/Content.
type reqBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type reqTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// chatResponse is a non-streaming /v1/messages reply.
type chatResponse struct {
	Content    []respBlock `json:"content"`
	StopReason string      `json:"stop_reason"`
	Usage      usage       `json:"usage"`
}

type respBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// toolBlock is the adapter-internal form of a tool call, shared by the
// streaming and non-streaming paths before mapping to gantry.ToolCall.
type toolBlock struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// defaultMaxTokens is used when the request leaves MaxTokens at 0. Anthropic
// requires a positive max_tokens, whereas harness treats 0 as "provider
// default", so the adapter supplies one.
const defaultMaxTokens = 4096

// toChatRequest maps a harness request to the Anthropic wire format. System is
// a top-level field (not a message), and tool results become user-role
// tool_result blocks.
func toChatRequest(model string, req gantry.LLMRequest, stream bool) chatRequest {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultMaxTokens
	}
	return chatRequest{
		Model:       model,
		System:      req.System,
		Messages:    toMessages(req.Messages),
		Tools:       toTools(req.Tools),
		MaxTokens:   maxTokens,
		Temperature: req.Temperature,
		Stream:      stream,
	}
}

// toMessages maps harness messages to Anthropic's user/assistant content-block
// form. Anthropic has only those two roles, so tool results are carried as
// tool_result blocks inside a user message; consecutive tool results are merged
// into a single user message as Anthropic expects.
func toMessages(in []gantry.Message) []reqMessage {
	var out []reqMessage
	lastToolResult := false
	for _, m := range in {
		switch m.Role {
		case gantry.RoleTool:
			block := reqBlock{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content}
			if lastToolResult {
				out[len(out)-1].Content = append(out[len(out)-1].Content, block)
			} else {
				out = append(out, reqMessage{Role: "user", Content: []reqBlock{block}})
				lastToolResult = true
			}
		case gantry.RoleAssistant:
			var blocks []reqBlock
			if m.Content != "" {
				blocks = append(blocks, reqBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, reqBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: tc.Input})
			}
			out = append(out, reqMessage{Role: "assistant", Content: blocks})
			lastToolResult = false
		default:
			out = append(out, reqMessage{Role: "user", Content: []reqBlock{{Type: "text", Text: m.Content}}})
			lastToolResult = false
		}
	}
	return out
}

func toTools(defs []gantry.ToolDef) []reqTool {
	if len(defs) == 0 {
		return nil
	}
	out := make([]reqTool, len(defs))
	for i, d := range defs {
		out[i] = reqTool{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: d.Schema,
		}
	}
	return out
}

// splitBlocks separates a non-streaming response's content blocks into the
// aggregated text and the tool calls.
func splitBlocks(blocks []respBlock) (string, []toolBlock) {
	var sb strings.Builder
	var calls []toolBlock
	for _, b := range blocks {
		switch b.Type {
		case "text":
			sb.WriteString(b.Text)
		case "tool_use":
			calls = append(calls, toolBlock{ID: b.ID, Name: b.Name, Input: b.Input})
		}
	}
	return sb.String(), calls
}

// assembleResponse builds the harness response from aggregated stream/non-stream
// fields. It is the single place stop-reason and tool-call mapping live.
func assembleResponse(content string, calls []toolBlock, stopReasonStr string, u usage) gantry.LLMResponse {
	return gantry.LLMResponse{
		Content:    content,
		ToolCalls:  toToolCalls(calls),
		StopReason: stopReason(stopReasonStr, len(calls) > 0),
		Usage:      gantry.Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens},
	}
}

func toToolCalls(calls []toolBlock) []gantry.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]gantry.ToolCall, len(calls))
	for i, c := range calls {
		out[i] = gantry.ToolCall{ID: c.ID, Name: c.Name, Input: c.Input}
	}
	return out
}

func stopReason(stopReasonStr string, hasTools bool) gantry.StopReason {
	switch {
	case hasTools || stopReasonStr == "tool_use":
		return gantry.StopReasonToolUse
	case stopReasonStr == "max_tokens":
		return gantry.StopReasonMaxTokens
	default:
		return gantry.StopReasonEnd
	}
}

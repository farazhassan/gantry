package agui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/farazhassan/gantry/harness"
)

// RunAgentInput is the AG-UI request body POSTed to the handler. v1 honors
// Messages (the replayed thread); State and Tools are accepted but ignored
// (client-supplied state-merge and client-advertised tools are out of scope).
type RunAgentInput struct {
	ThreadID string          `json:"threadId"`
	RunID    string          `json:"runId"`
	Messages []InputMessage  `json:"messages"`
	State    json.RawMessage `json:"state,omitempty"`
	Tools    json.RawMessage `json:"tools,omitempty"`
}

// InputMessage is one entry in RunAgentInput.Messages. Tool linkage uses the
// OpenAI-style shape AG-UI adopts: assistant messages may carry ToolCalls, and
// tool messages carry ToolCallID.
type InputMessage struct {
	ID         string          `json:"id,omitempty"`
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	ToolCalls  []InputToolCall `json:"toolCalls,omitempty"`
}

// InputToolCall is an assistant tool-call request in the replayed history.
type InputToolCall struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Function InputToolFunction `json:"function"`
}

// InputToolFunction is the function name + raw JSON argument string.
type InputToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToRun reconstructs the prior conversation State and extracts the final user
// message for harness.RunFromStream. Because RunFromStream appends its input as
// a fresh user message, prior.Messages holds the history EXCLUDING that final
// turn and input is the final turn's content.
//
// It errors if Messages is empty, the last message is not a user turn, or any
// history message has an unrecognized role (so context is never silently lost).
func (in *RunAgentInput) ToRun() (prior *harness.State, input string, err error) {
	if len(in.Messages) == 0 {
		return nil, "", errors.New("agui: messages is empty")
	}
	last := in.Messages[len(in.Messages)-1]
	if last.Role != "user" {
		return nil, "", fmt.Errorf("agui: last message role = %q, want \"user\"", last.Role)
	}
	head := in.Messages[:len(in.Messages)-1]
	msgs := make([]harness.Message, 0, len(head))
	for i := range head {
		m, err := toHarnessMessage(head[i])
		if err != nil {
			return nil, "", err
		}
		msgs = append(msgs, m)
	}
	return &harness.State{Messages: msgs}, last.Content, nil
}

// toHarnessMessage maps one AG-UI input message to a harness.Message, mapping
// roles and tool linkage. Unknown roles are an error.
func toHarnessMessage(im InputMessage) (harness.Message, error) {
	var role harness.Role
	switch im.Role {
	case "system":
		role = harness.RoleSystem
	case "user":
		role = harness.RoleUser
	case "assistant":
		role = harness.RoleAssistant
	case "tool":
		role = harness.RoleTool
	default:
		return harness.Message{}, fmt.Errorf("agui: unknown message role %q", im.Role)
	}

	// Enforce role/tool-call linkage invariants. The handler is a trust
	// boundary, and silently corrupting tool linkage in the reconstructed
	// transcript is worse than a clear 400 (mirrors the unknown-role error).
	if len(im.ToolCalls) > 0 && role != harness.RoleAssistant {
		return harness.Message{}, fmt.Errorf("agui: only assistant messages may carry toolCalls, got role %q", im.Role)
	}
	if role == harness.RoleTool && im.ToolCallID == "" {
		return harness.Message{}, errors.New("agui: tool message missing toolCallId")
	}
	if role != harness.RoleTool && im.ToolCallID != "" {
		return harness.Message{}, fmt.Errorf("agui: only tool messages may set toolCallId, got role %q", im.Role)
	}

	m := harness.Message{
		Role:       role,
		Content:    im.Content,
		Name:       im.Name,
		ToolCallID: im.ToolCallID,
	}
	for _, tc := range im.ToolCalls {
		if tc.ID == "" {
			return harness.Message{}, errors.New("agui: tool call missing id")
		}
		if tc.Function.Name == "" {
			return harness.Message{}, fmt.Errorf("agui: tool call %q missing function name", tc.ID)
		}
		// Type is optional; AG-UI/OpenAI only define "function" today, so accept
		// an empty type but reject anything else rather than mapping a shape we
		// can't represent.
		if tc.Type != "" && tc.Type != "function" {
			return harness.Message{}, fmt.Errorf("agui: tool call %q has unsupported type %q", tc.ID, tc.Type)
		}
		// Arguments is a JSON string per the AG-UI/OpenAI shape. Forwarding
		// invalid JSON into a json.RawMessage would silently corrupt the call
		// and later break adapter (un)marshaling, so reject it up front. An
		// empty string means "no arguments" and maps to a nil Input.
		var input json.RawMessage
		if args := strings.TrimSpace(tc.Function.Arguments); args != "" {
			if !json.Valid([]byte(args)) {
				return harness.Message{}, fmt.Errorf("agui: tool call %q has invalid JSON arguments", tc.ID)
			}
			input = json.RawMessage(tc.Function.Arguments)
		}
		m.ToolCalls = append(m.ToolCalls, harness.ToolCall{
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}
	return m, nil
}

package agui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/farazhassan/gantry"
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
// message for gantry.RunFromStream. Because RunFromStream appends its input as
// a fresh user message, prior.Messages holds the history EXCLUDING that final
// turn and input is the final turn's content.
//
// It errors if Messages is empty, the last message is not a user turn, or any
// history message has an unrecognized role (so context is never silently lost).
func (in *RunAgentInput) ToRun() (prior *gantry.State, input string, err error) {
	if len(in.Messages) == 0 {
		return nil, "", errors.New("agui: messages is empty")
	}
	last := in.Messages[len(in.Messages)-1]
	if last.Role != "user" {
		return nil, "", fmt.Errorf("agui: last message role = %q, want \"user\"", last.Role)
	}
	head := in.Messages[:len(in.Messages)-1]
	msgs := make([]gantry.Message, 0, len(head))
	for i := range head {
		m, err := toHarnessMessage(head[i])
		if err != nil {
			return nil, "", err
		}
		msgs = append(msgs, m)
	}
	return &gantry.State{Messages: msgs}, last.Content, nil
}

// ToResume reconstructs the full prior conversation as a non-terminal State for
// gantry.ResumeStream. Unlike ToRun, it keeps the final message — a tool-role
// result fulfilling a suspended client tool call — in the transcript and adds
// no fresh user input. The handler uses it when the posted history is
// tool-result-terminated (the AG-UI human-in-the-loop resume signal).
//
// It errors if Messages is empty, any message has an unrecognized role or
// invalid tool linkage (same invariants as ToRun), or any assistant tool call
// lacks a matching tool result — resuming with an outstanding call would
// surface as a provider error mid-stream.
func (in *RunAgentInput) ToResume() (*gantry.State, error) {
	if len(in.Messages) == 0 {
		return nil, errors.New("agui: messages is empty")
	}
	msgs := make([]gantry.Message, 0, len(in.Messages))
	for i := range in.Messages {
		m, err := toHarnessMessage(in.Messages[i])
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	if err := requireToolResults(msgs); err != nil {
		return nil, err
	}
	// ResumeStream runs this State directly (no newStateFrom rebuild), so Meta
	// and Trace must be initialized here. Meta is needed by the client-tools
	// advertise middleware (it assigns into the map); Trace is needed by run().
	return &gantry.State{
		Messages: msgs,
		Meta:     map[string]any{},
		Trace:    gantry.NewTrace(),
	}, nil
}

// requireToolResults verifies every assistant tool call id has a matching
// tool-role result later in the transcript. Without this, an outstanding
// client tool call would make the next provider request invalid.
func requireToolResults(msgs []gantry.Message) error {
	fulfilled := map[string]bool{}
	for _, m := range msgs {
		if m.Role == gantry.RoleTool && m.ToolCallID != "" {
			fulfilled[m.ToolCallID] = true
		}
	}
	for _, m := range msgs {
		if m.Role != gantry.RoleAssistant {
			continue
		}
		for _, tc := range m.ToolCalls {
			if !fulfilled[tc.ID] {
				return fmt.Errorf("agui: tool call %q has no matching tool result; cannot resume", tc.ID)
			}
		}
	}
	return nil
}

// toHarnessMessage maps one AG-UI input message to a gantry.Message, mapping
// roles and tool linkage. Unknown roles are an error.
func toHarnessMessage(im InputMessage) (gantry.Message, error) {
	var role gantry.Role
	switch im.Role {
	case "system":
		role = gantry.RoleSystem
	case "user":
		role = gantry.RoleUser
	case "assistant":
		role = gantry.RoleAssistant
	case "tool":
		role = gantry.RoleTool
	default:
		return gantry.Message{}, fmt.Errorf("agui: unknown message role %q", im.Role)
	}

	// Enforce role/tool-call linkage invariants. The handler is a trust
	// boundary, and silently corrupting tool linkage in the reconstructed
	// transcript is worse than a clear 400 (mirrors the unknown-role error).
	if len(im.ToolCalls) > 0 && role != gantry.RoleAssistant {
		return gantry.Message{}, fmt.Errorf("agui: only assistant messages may carry toolCalls, got role %q", im.Role)
	}
	if role == gantry.RoleTool && im.ToolCallID == "" {
		return gantry.Message{}, errors.New("agui: tool message missing toolCallId")
	}
	if role != gantry.RoleTool && im.ToolCallID != "" {
		return gantry.Message{}, fmt.Errorf("agui: only tool messages may set toolCallId, got role %q", im.Role)
	}

	m := gantry.Message{
		Role:       role,
		Content:    im.Content,
		Name:       im.Name,
		ToolCallID: im.ToolCallID,
	}
	for _, tc := range im.ToolCalls {
		if tc.ID == "" {
			return gantry.Message{}, errors.New("agui: tool call missing id")
		}
		if tc.Function.Name == "" {
			return gantry.Message{}, fmt.Errorf("agui: tool call %q missing function name", tc.ID)
		}
		// Type is optional; AG-UI/OpenAI only define "function" today, so accept
		// an empty type but reject anything else rather than mapping a shape we
		// can't represent.
		if tc.Type != "" && tc.Type != "function" {
			return gantry.Message{}, fmt.Errorf("agui: tool call %q has unsupported type %q", tc.ID, tc.Type)
		}
		// Arguments is a JSON string per the AG-UI/OpenAI shape. Forwarding
		// invalid JSON into a json.RawMessage would silently corrupt the call
		// and later break adapter (un)marshaling, so reject it up front. An
		// empty string means "no arguments" and maps to a nil Input.
		var input json.RawMessage
		if args := strings.TrimSpace(tc.Function.Arguments); args != "" {
			if !json.Valid([]byte(args)) {
				return gantry.Message{}, fmt.Errorf("agui: tool call %q has invalid JSON arguments", tc.ID)
			}
			input = json.RawMessage(tc.Function.Arguments)
		}
		m.ToolCalls = append(m.ToolCalls, gantry.ToolCall{
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}
	return m, nil
}

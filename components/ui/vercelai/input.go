package vercelai

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/farazhassan/gantry"
)

// ChatRequest is the request body useChat() POSTs to the server: the full
// UIMessage history plus bookkeeping fields v1 does not trust -- see
// ToRun's doc comment for why Trigger is ignored.
type ChatRequest struct {
	ID       string      `json:"id"`
	Messages []UIMessage `json:"messages"`
	Trigger  string      `json:"trigger,omitempty"`
}

// UIMessage is one entry in ChatRequest.Messages.
type UIMessage struct {
	ID    string `json:"id,omitempty"`
	Role  string `json:"role"` // system | user | assistant
	Parts []Part `json:"parts"`
}

// Part is one entry in UIMessage.Parts. Only the fields relevant to Type
// are set; decoding into this flat struct needs no custom UnmarshalJSON,
// since JSON simply leaves fields absent from a given part's wire shape at
// their zero value.
type Part struct {
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	ToolName   string          `json:"toolName,omitempty"`
	State      string          `json:"state,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
	Output     json.RawMessage `json:"output,omitempty"`
	ErrorText  string          `json:"errorText,omitempty"`
}

// Part type discriminators this package understands by exact match.
// "tool-<name>" static-tool parts are matched with isToolPart below rather
// than a constant, since tool part type names are dynamic.
const (
	partText        = "text"
	partReasoning   = "reasoning"
	partStepStart   = "step-start"
	partDynamicTool = "dynamic-tool"
	partFile        = "file"
	partSourceURL   = "source-url"
	partSourceDoc   = "source-document"
	dataPartPrefix  = "data-"
	toolPartPrefix  = "tool-"
)

// Tool part State values this package understands.
const (
	stateInputStreaming  = "input-streaming"
	stateInputAvailable  = "input-available"
	stateOutputAvailable = "output-available"
	stateOutputError     = "output-error"
	stateOutputDenied    = "output-denied"
)

// isToolPart reports whether t is a tool-call part: the SDK's own
// "dynamic-tool" (a tool whose name isn't known until runtime -- what
// every Gantry tool is, from this package's point of view) or a static
// "tool-<name>" part emitted by clients that used the SDK's typed tool
// helpers on a previous turn.
func isToolPart(t string) bool {
	return t == partDynamicTool || strings.HasPrefix(t, toolPartPrefix)
}

// userText concatenates m's text parts into gantry's single Content
// string. A "file" part is an error (real user-authored content that
// gantry.Message cannot represent and would otherwise silently vanish); a
// tool part is an error (only assistant messages carry tool calls);
// reasoning/source-*/data-*/step-start parts are silently skipped (not
// model-input content). Also used for "system" role messages, which
// follow the same part-kind rules.
func userText(m UIMessage) (string, error) {
	var sb strings.Builder
	for _, p := range m.Parts {
		switch {
		case p.Type == partText:
			sb.WriteString(p.Text)
		case p.Type == partFile:
			return "", errors.New("vercelai: message contains a file part, which gantry cannot represent")
		case isToolPart(p.Type):
			return "", fmt.Errorf("vercelai: message contains a tool part (type %q); only assistant messages may carry tool calls", p.Type)
		case p.Type == partReasoning, p.Type == partSourceURL, p.Type == partSourceDoc, p.Type == partStepStart, strings.HasPrefix(p.Type, dataPartPrefix):
			// skipped: not model-input content
		default:
			return "", fmt.Errorf("vercelai: message contains unrecognized part type %q", p.Type)
		}
	}
	return sb.String(), nil
}

// toGantryMessages maps one UIMessage to one-or-more gantry.Message
// values. system and user messages always map to exactly one
// gantry.Message. An assistant message maps to one gantry.Message per
// "step" -- see segmentAssistant.
func toGantryMessages(m UIMessage) ([]gantry.Message, error) {
	switch m.Role {
	case "system":
		text, err := userText(m)
		if err != nil {
			return nil, err
		}
		return []gantry.Message{{Role: gantry.RoleSystem, Content: text}}, nil
	case "user":
		text, err := userText(m)
		if err != nil {
			return nil, err
		}
		return []gantry.Message{{Role: gantry.RoleUser, Content: text}}, nil
	case "assistant":
		return segmentAssistant(m)
	default:
		return nil, fmt.Errorf("vercelai: unknown message role %q", m.Role)
	}
}

// segmentAssistant splits one assistant UIMessage into gantry.Message(s)
// at each "step-start" part: within a segment, its text parts concatenate
// into one gantry.Message{Role: RoleAssistant}, its tool parts become that
// message's ToolCalls, and each *resolved* tool part additionally
// produces a following gantry.Message{Role: RoleTool} carrying the
// result, in order. A message with no step-start parts is a single
// segment -- the common case for a plain single-call turn.
//
// This split exists because the AI SDK's own multi-step tool loop bundles
// every step of one generateText/streamText call into ONE UIMessage,
// while gantry.Message can only represent a single assistant turn (one
// Content + one ToolCalls list).
func segmentAssistant(m UIMessage) ([]gantry.Message, error) {
	var out []gantry.Message
	var text strings.Builder
	var calls []gantry.ToolCall
	var results []gantry.Message

	flush := func() {
		if text.Len() == 0 && len(calls) == 0 {
			return
		}
		out = append(out, gantry.Message{
			Role:      gantry.RoleAssistant,
			Content:   text.String(),
			ToolCalls: calls,
		})
		out = append(out, results...)
		text.Reset()
		calls = nil
		results = nil
	}

	for _, p := range m.Parts {
		switch {
		case p.Type == partStepStart:
			flush()
		case p.Type == partText:
			text.WriteString(p.Text)
		case isToolPart(p.Type):
			call, result, err := toolCallAndResult(p)
			if err != nil {
				return nil, err
			}
			calls = append(calls, call)
			if result != nil {
				results = append(results, *result)
			}
		case p.Type == partReasoning, p.Type == partSourceURL, p.Type == partSourceDoc, strings.HasPrefix(p.Type, dataPartPrefix):
			// skipped: not model-input content
		case p.Type == partFile:
			// An assistant-authored file reference (e.g. a generated
			// image) has no gantry.Message representation either, but
			// unlike a user's file it is not something the user typed --
			// dropping it loses a rendering detail, not conversational
			// intent, so this is skipped rather than an error.
		default:
			return nil, fmt.Errorf("vercelai: assistant message contains unrecognized part type %q", p.Type)
		}
	}
	flush()
	return out, nil
}

// toolCallAndResult maps one tool part to a gantry.ToolCall plus, if the
// part carries a resolved result, the gantry.Message{Role: RoleTool} that
// answers it. A part in state input-streaming or input-available (no
// result yet) returns a nil result -- valid only when this is part of the
// LAST message of the LAST request (the resume-suspension case); anywhere
// else, requireToolResults rejects the resulting unanswered call.
func toolCallAndResult(p Part) (gantry.ToolCall, *gantry.Message, error) {
	if p.ToolCallID == "" {
		return gantry.ToolCall{}, nil, errors.New("vercelai: tool part missing toolCallId")
	}
	if p.ToolName == "" {
		return gantry.ToolCall{}, nil, fmt.Errorf("vercelai: tool part %q missing toolName", p.ToolCallID)
	}
	if len(p.Input) > 0 && !json.Valid(p.Input) {
		return gantry.ToolCall{}, nil, fmt.Errorf("vercelai: tool part %q has invalid JSON input", p.ToolCallID)
	}
	call := gantry.ToolCall{ID: p.ToolCallID, Name: p.ToolName, Input: p.Input}

	// Output is kept as raw JSON text (quotes and all, for a string
	// output) rather than unwrapped: gantry.ToolResult.Content has no
	// structured-vs-string distinction, and unwrapping is ill-defined
	// when Output isn't a JSON string to begin with.
	switch p.State {
	case stateInputStreaming, stateInputAvailable:
		return call, nil, nil
	case stateOutputAvailable:
		return call, &gantry.Message{Role: gantry.RoleTool, ToolCallID: p.ToolCallID, Content: string(p.Output)}, nil
	case stateOutputError:
		return call, &gantry.Message{Role: gantry.RoleTool, ToolCallID: p.ToolCallID, Content: p.ErrorText}, nil
	case stateOutputDenied:
		return call, &gantry.Message{Role: gantry.RoleTool, ToolCallID: p.ToolCallID, Content: "tool call denied by user"}, nil
	default:
		return gantry.ToolCall{}, nil, fmt.Errorf("vercelai: tool part %q has unrecognized state %q", p.ToolCallID, p.State)
	}
}

// requireToolResults validates that msgs is a well-formed transcript: each
// assistant tool call must be answered by a following tool-role message
// with a matching ToolCallID, with no duplicate or out-of-order linkage,
// and none left open at the end. An unanswered or misordered tool call
// would make the next provider request invalid, so this is caught here as
// a clean 400 rather than failing mid-stream.
func requireToolResults(msgs []gantry.Message) error {
	open := map[string]bool{} // tool-call ids seen but not yet answered
	seen := map[string]bool{} // every tool-call id ever introduced (dup guard)
	for _, m := range msgs {
		switch m.Role {
		case gantry.RoleAssistant:
			for _, tc := range m.ToolCalls {
				if seen[tc.ID] {
					return fmt.Errorf("vercelai: duplicate tool call id %q", tc.ID)
				}
				seen[tc.ID] = true
				open[tc.ID] = true
			}
		case gantry.RoleTool:
			if !open[m.ToolCallID] {
				return fmt.Errorf("vercelai: tool result for %q has no preceding unanswered tool call", m.ToolCallID)
			}
			delete(open, m.ToolCallID)
		}
	}
	for id := range open {
		return fmt.Errorf("vercelai: tool call %q has no matching tool result", id)
	}
	return nil
}

// ToRun reconstructs the prior conversation State and extracts the final
// turn's input text for gantry.RunFromStream. Because RunFromStream
// appends its input as a fresh user message, prior.Messages holds the
// history EXCLUDING that final turn and input is the final turn's
// concatenated text.
//
// ChatRequest.Trigger is never consulted: this package derives run-vs-resume
// from the message list's own shape, not from a client-supplied hint --
// trusting Trigger would let a malformed or malicious client desync the
// reconstructed transcript from what the model actually produced.
//
// It errors if Messages is empty, the last message is not role "user", any
// history message fails toGantryMessages, or the history has unbalanced
// tool-call linkage (see requireToolResults).
func (r *ChatRequest) ToRun() (prior *gantry.State, input string, err error) {
	if len(r.Messages) == 0 {
		return nil, "", errors.New("vercelai: messages is empty")
	}
	last := r.Messages[len(r.Messages)-1]
	if last.Role != "user" {
		return nil, "", fmt.Errorf("vercelai: last message role = %q, want \"user\"", last.Role)
	}
	inputText, err := userText(last)
	if err != nil {
		return nil, "", err
	}

	head := r.Messages[:len(r.Messages)-1]
	var msgs []gantry.Message
	for i := range head {
		mm, err := toGantryMessages(head[i])
		if err != nil {
			return nil, "", err
		}
		msgs = append(msgs, mm...)
	}
	if err := requireToolResults(msgs); err != nil {
		return nil, "", err
	}
	return &gantry.State{Messages: msgs}, inputText, nil
}

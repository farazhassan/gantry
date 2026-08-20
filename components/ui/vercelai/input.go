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

// decodeToolOutput reconstructs a resolved tool part's Output back into a
// plain gantry.ToolResult.Content string. newToolOutputAvailable (chunks.go)
// always JSON-encodes ToolResult.Content as a wire JSON *string* -- so if
// raw unmarshals as a Go string, that's the original Content value
// recovered exactly. If raw isn't a JSON string (e.g. a client or test
// hand-authored a bare object/number as Output), fall back to the raw JSON
// text as-is, since there's no string to unwrap.
func decodeToolOutput(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
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

	// Output is decoded via decodeToolOutput rather than taken as raw JSON
	// text: newToolOutputAvailable (chunks.go) always JSON-encodes
	// gantry.ToolResult.Content as a wire JSON *string*, so undoing that
	// encoding recovers the original Content exactly. See decodeToolOutput
	// for the fallback when Output isn't a JSON string to begin with (e.g.
	// a hand-authored test part).
	switch p.State {
	case stateInputStreaming, stateInputAvailable:
		return call, nil, nil
	case stateOutputAvailable:
		return call, &gantry.Message{Role: gantry.RoleTool, ToolCallID: p.ToolCallID, Content: decodeToolOutput(p.Output)}, nil
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

// ToResume reconstructs the full prior conversation, including the final
// message, as a non-terminal State for gantry.ResumeStream. The handler
// uses it when the message list ends in an assistant turn whose tool
// call(s) are now resolved -- this protocol's equivalent of AG-UI's
// tool-role-terminated resume signal (there is no separate "tool" role
// here; see doc.go's "Run vs. resume" note).
//
// It errors under the same conditions as ToRun (empty Messages, unknown
// role, invalid tool linkage), applied to the FULL message list including
// the last one -- so an unresolved tool call anywhere, including the very
// last message, is caught by requireToolResults -- plus: a last message
// with no tool part at all (nothing to resume).
func (r *ChatRequest) ToResume() (*gantry.State, error) {
	if len(r.Messages) == 0 {
		return nil, errors.New("vercelai: messages is empty")
	}
	last := r.Messages[len(r.Messages)-1]
	if !hasToolPart(last) {
		return nil, errors.New("vercelai: last message has no tool call to resume")
	}

	var msgs []gantry.Message
	for i := range r.Messages {
		mm, err := toGantryMessages(r.Messages[i])
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, mm...)
	}
	if err := requireToolResults(msgs); err != nil {
		return nil, err
	}
	// ResumeStream runs this State directly (no rebuild from a fresh
	// input), so Meta and Trace must be initialized here -- mirrors agui's
	// ToResume, which needs the same for the client-tools advertise
	// middleware (Meta) and the run loop (Trace).
	return &gantry.State{
		Messages: msgs,
		Meta:     map[string]any{},
		Trace:    gantry.NewTrace(),
	}, nil
}

// hasToolPart reports whether m's FINAL step segment (the parts after its
// last "step-start", or all parts if it has none) contains at least one
// tool-call part -- the signal Handler uses to route a request to
// ResumeStream instead of RunFromStream. Scoped to the final segment only:
// an earlier step's tool call says nothing about whether the turn as a
// whole is still suspended -- see segmentAssistant's doc comment for why a
// single UIMessage can span multiple steps.
func hasToolPart(m UIMessage) bool {
	for i := len(m.Parts) - 1; i >= 0; i-- {
		p := m.Parts[i]
		if p.Type == partStepStart {
			return false
		}
		if isToolPart(p.Type) {
			return true
		}
	}
	return false
}

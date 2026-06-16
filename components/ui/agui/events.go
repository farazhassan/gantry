package agui

import (
	"encoding/json"
	"fmt"
	"io"
)

// Event is one AG-UI protocol event. Concrete types marshal to the exact AG-UI
// wire JSON: a SCREAMING_SNAKE_CASE "type" discriminator plus camelCase fields.
type Event interface {
	// eventType returns the wire "type" value; it also keeps the Event
	// interface closed to this package.
	eventType() string
}

// AG-UI event type discriminators.
const (
	typeRunStarted         = "RUN_STARTED"
	typeRunFinished        = "RUN_FINISHED"
	typeRunError           = "RUN_ERROR"
	typeStepStarted        = "STEP_STARTED"
	typeStepFinished       = "STEP_FINISHED"
	typeTextMessageStart   = "TEXT_MESSAGE_START"
	typeTextMessageContent = "TEXT_MESSAGE_CONTENT"
	typeTextMessageEnd     = "TEXT_MESSAGE_END"
	typeToolCallStart      = "TOOL_CALL_START"
	typeToolCallArgs       = "TOOL_CALL_ARGS"
	typeToolCallEnd        = "TOOL_CALL_END"
	typeToolCallResult     = "TOOL_CALL_RESULT"
)

// --- Lifecycle ---

type RunStarted struct {
	Type     string `json:"type"`
	ThreadID string `json:"threadId"`
	RunID    string `json:"runId"`
}

func (e RunStarted) eventType() string { return e.Type }
func newRunStarted(threadID, runID string) RunStarted {
	return RunStarted{Type: typeRunStarted, ThreadID: threadID, RunID: runID}
}

type RunFinished struct {
	Type     string `json:"type"`
	ThreadID string `json:"threadId"`
	RunID    string `json:"runId"`
}

func (e RunFinished) eventType() string { return e.Type }
func newRunFinished(threadID, runID string) RunFinished {
	return RunFinished{Type: typeRunFinished, ThreadID: threadID, RunID: runID}
}

type RunError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (e RunError) eventType() string { return e.Type }
func newRunError(msg string) RunError {
	return RunError{Type: typeRunError, Message: msg}
}

type StepStarted struct {
	Type     string `json:"type"`
	StepName string `json:"stepName"`
}

func (e StepStarted) eventType() string { return e.Type }
func newStepStarted(name string) StepStarted {
	return StepStarted{Type: typeStepStarted, StepName: name}
}

type StepFinished struct {
	Type     string `json:"type"`
	StepName string `json:"stepName"`
}

func (e StepFinished) eventType() string { return e.Type }
func newStepFinished(name string) StepFinished {
	return StepFinished{Type: typeStepFinished, StepName: name}
}

// --- Text messages ---

type TextMessageStart struct {
	Type      string `json:"type"`
	MessageID string `json:"messageId"`
	Role      string `json:"role"`
}

func (e TextMessageStart) eventType() string { return e.Type }
func newTextMessageStart(msgID string) TextMessageStart {
	return TextMessageStart{Type: typeTextMessageStart, MessageID: msgID, Role: "assistant"}
}

type TextMessageContent struct {
	Type      string `json:"type"`
	MessageID string `json:"messageId"`
	Delta     string `json:"delta"`
}

func (e TextMessageContent) eventType() string { return e.Type }
func newTextMessageContent(msgID, delta string) TextMessageContent {
	return TextMessageContent{Type: typeTextMessageContent, MessageID: msgID, Delta: delta}
}

type TextMessageEnd struct {
	Type      string `json:"type"`
	MessageID string `json:"messageId"`
}

func (e TextMessageEnd) eventType() string { return e.Type }
func newTextMessageEnd(msgID string) TextMessageEnd {
	return TextMessageEnd{Type: typeTextMessageEnd, MessageID: msgID}
}

// --- Tool calls ---

type ToolCallStart struct {
	Type         string `json:"type"`
	ToolCallID   string `json:"toolCallId"`
	ToolCallName string `json:"toolCallName"`
}

func (e ToolCallStart) eventType() string { return e.Type }
func newToolCallStart(id, name string) ToolCallStart {
	return ToolCallStart{Type: typeToolCallStart, ToolCallID: id, ToolCallName: name}
}

type ToolCallArgs struct {
	Type       string `json:"type"`
	ToolCallID string `json:"toolCallId"`
	Delta      string `json:"delta"`
}

func (e ToolCallArgs) eventType() string { return e.Type }
func newToolCallArgs(id, delta string) ToolCallArgs {
	return ToolCallArgs{Type: typeToolCallArgs, ToolCallID: id, Delta: delta}
}

type ToolCallEnd struct {
	Type       string `json:"type"`
	ToolCallID string `json:"toolCallId"`
}

func (e ToolCallEnd) eventType() string { return e.Type }
func newToolCallEnd(id string) ToolCallEnd {
	return ToolCallEnd{Type: typeToolCallEnd, ToolCallID: id}
}

type ToolCallResult struct {
	Type       string `json:"type"`
	MessageID  string `json:"messageId"`
	ToolCallID string `json:"toolCallId"`
	Content    string `json:"content"`
	Role       string `json:"role"`
}

func (e ToolCallResult) eventType() string { return e.Type }
func newToolCallResult(msgID, toolCallID, content string) ToolCallResult {
	return ToolCallResult{Type: typeToolCallResult, MessageID: msgID, ToolCallID: toolCallID, Content: content, Role: "tool"}
}

// WriteSSE marshals ev and writes it as one Server-Sent Events frame:
// "data: <json>\n\n". The AG-UI discriminator lives in the JSON "type" field,
// so no SSE "event:" line is emitted.
func WriteSSE(w io.Writer, ev Event) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", b)
	return err
}

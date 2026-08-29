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
	// withIdentity returns a copy of the event with id attached. Lifecycle
	// events (RunStarted/RunFinished/RunError) ignore id and return
	// themselves unchanged — they describe the single top-level AG-UI run,
	// not a Gantry-level run/agent.
	withIdentity(id identity) Event
}

// identity is embedded (anonymously, so its fields flatten into the
// containing event's JSON) into every AG-UI event EXCEPT the three
// lifecycle events (RunStarted/RunFinished/RunError), which describe the
// single top-level AG-UI run/thread and have their own unrelated
// "runId"/"threadId" fields already. It carries the Gantry-level
// attribution — which run, which agent, and (for a nested sub-agent run)
// which parent tool call spawned it — so a client can correctly demux
// events from concurrent/interleaved parent and sub-agent runs on one SSE
// stream.
type identity struct {
	RunID            string `json:"runId,omitempty"`
	SessionID        string `json:"sessionId,omitempty"`
	TaskID           string `json:"taskId,omitempty"`
	Agent            string `json:"agent,omitempty"`
	ParentRunID      string `json:"parentRunId,omitempty"`
	ParentToolCallID string `json:"parentToolCallId,omitempty"`

	// SubagentRunID is the AG-UI spec's flat attribution field: the RunID of
	// the nested sub-agent run that produced this event, empty for a
	// top-level event. A generic AG-UI client that only understands the
	// spec field (not gantry's richer identity fields above) can use this
	// alone to demux concurrent sub-agent output. Not yet populated by
	// Mapper.Map -- that wiring lands in a later task (see
	// docs/superpowers/plans/2026-08-29-agui-subagent-lifecycle-events.md).
	SubagentRunID string `json:"subagentRunId,omitempty"`
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

	typeReasoningStart          = "REASONING_START"
	typeReasoningMessageStart   = "REASONING_MESSAGE_START"
	typeReasoningMessageContent = "REASONING_MESSAGE_CONTENT"
	typeReasoningMessageEnd     = "REASONING_MESSAGE_END"
	typeReasoningEnd            = "REASONING_END"

	typeToolCallStart  = "TOOL_CALL_START"
	typeToolCallArgs   = "TOOL_CALL_ARGS"
	typeToolCallEnd    = "TOOL_CALL_END"
	typeToolCallResult = "TOOL_CALL_RESULT"
	typeCustom         = "CUSTOM"
	typeRaw            = "RAW"

	typeActivitySnapshot = "ACTIVITY_SNAPSHOT"
	typeActivityDelta    = "ACTIVITY_DELTA"
)

// --- Lifecycle ---

type RunStarted struct {
	Type     string `json:"type"`
	ThreadID string `json:"threadId"`
	RunID    string `json:"runId"`
}

func (e RunStarted) eventType() string           { return e.Type }
func (e RunStarted) withIdentity(identity) Event { return e }
func newRunStarted(threadID, runID string) RunStarted {
	return RunStarted{Type: typeRunStarted, ThreadID: threadID, RunID: runID}
}

type RunFinished struct {
	Type     string `json:"type"`
	ThreadID string `json:"threadId"`
	RunID    string `json:"runId"`
}

func (e RunFinished) eventType() string           { return e.Type }
func (e RunFinished) withIdentity(identity) Event { return e }
func newRunFinished(threadID, runID string) RunFinished {
	return RunFinished{Type: typeRunFinished, ThreadID: threadID, RunID: runID}
}

type RunError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (e RunError) eventType() string           { return e.Type }
func (e RunError) withIdentity(identity) Event { return e }
func newRunError(msg string) RunError {
	return RunError{Type: typeRunError, Message: msg}
}

type StepStarted struct {
	Type     string `json:"type"`
	StepName string `json:"stepName"`
	identity
}

func (e StepStarted) eventType() string { return e.Type }
func (e StepStarted) withIdentity(id identity) Event {
	e.identity = id
	return e
}
func newStepStarted(name string) StepStarted {
	return StepStarted{Type: typeStepStarted, StepName: name}
}

type StepFinished struct {
	Type     string `json:"type"`
	StepName string `json:"stepName"`
	identity
}

func (e StepFinished) eventType() string { return e.Type }
func (e StepFinished) withIdentity(id identity) Event {
	e.identity = id
	return e
}
func newStepFinished(name string) StepFinished {
	return StepFinished{Type: typeStepFinished, StepName: name}
}

// --- Text messages ---

type TextMessageStart struct {
	Type      string `json:"type"`
	MessageID string `json:"messageId"`
	Role      string `json:"role"`
	identity
}

func (e TextMessageStart) eventType() string { return e.Type }
func (e TextMessageStart) withIdentity(id identity) Event {
	e.identity = id
	return e
}
func newTextMessageStart(msgID string) TextMessageStart {
	return TextMessageStart{Type: typeTextMessageStart, MessageID: msgID, Role: "assistant"}
}

type TextMessageContent struct {
	Type      string `json:"type"`
	MessageID string `json:"messageId"`
	Delta     string `json:"delta"`
	identity
}

func (e TextMessageContent) eventType() string { return e.Type }
func (e TextMessageContent) withIdentity(id identity) Event {
	e.identity = id
	return e
}
func newTextMessageContent(msgID, delta string) TextMessageContent {
	return TextMessageContent{Type: typeTextMessageContent, MessageID: msgID, Delta: delta}
}

type TextMessageEnd struct {
	Type      string `json:"type"`
	MessageID string `json:"messageId"`
	identity
}

func (e TextMessageEnd) eventType() string { return e.Type }
func (e TextMessageEnd) withIdentity(id identity) Event {
	e.identity = id
	return e
}
func newTextMessageEnd(msgID string) TextMessageEnd {
	return TextMessageEnd{Type: typeTextMessageEnd, MessageID: msgID}
}

// --- Reasoning ---

type ReasoningStart struct {
	Type      string `json:"type"`
	MessageID string `json:"messageId"`
	identity
}

func (e ReasoningStart) eventType() string { return e.Type }
func (e ReasoningStart) withIdentity(id identity) Event {
	e.identity = id
	return e
}
func newReasoningStart(msgID string) ReasoningStart {
	return ReasoningStart{Type: typeReasoningStart, MessageID: msgID}
}

type ReasoningMessageStart struct {
	Type      string `json:"type"`
	MessageID string `json:"messageId"`
	Role      string `json:"role"`
	identity
}

func (e ReasoningMessageStart) eventType() string { return e.Type }
func (e ReasoningMessageStart) withIdentity(id identity) Event {
	e.identity = id
	return e
}
func newReasoningMessageStart(msgID string) ReasoningMessageStart {
	return ReasoningMessageStart{Type: typeReasoningMessageStart, MessageID: msgID, Role: "reasoning"}
}

type ReasoningMessageContent struct {
	Type      string `json:"type"`
	MessageID string `json:"messageId"`
	Delta     string `json:"delta"`
	identity
}

func (e ReasoningMessageContent) eventType() string { return e.Type }
func (e ReasoningMessageContent) withIdentity(id identity) Event {
	e.identity = id
	return e
}
func newReasoningMessageContent(msgID, delta string) ReasoningMessageContent {
	return ReasoningMessageContent{Type: typeReasoningMessageContent, MessageID: msgID, Delta: delta}
}

type ReasoningMessageEnd struct {
	Type      string `json:"type"`
	MessageID string `json:"messageId"`
	identity
}

func (e ReasoningMessageEnd) eventType() string { return e.Type }
func (e ReasoningMessageEnd) withIdentity(id identity) Event {
	e.identity = id
	return e
}
func newReasoningMessageEnd(msgID string) ReasoningMessageEnd {
	return ReasoningMessageEnd{Type: typeReasoningMessageEnd, MessageID: msgID}
}

type ReasoningEnd struct {
	Type      string `json:"type"`
	MessageID string `json:"messageId"`
	identity
}

func (e ReasoningEnd) eventType() string { return e.Type }
func (e ReasoningEnd) withIdentity(id identity) Event {
	e.identity = id
	return e
}
func newReasoningEnd(msgID string) ReasoningEnd {
	return ReasoningEnd{Type: typeReasoningEnd, MessageID: msgID}
}

// --- Tool calls ---

type ToolCallStart struct {
	Type         string `json:"type"`
	ToolCallID   string `json:"toolCallId"`
	ToolCallName string `json:"toolCallName"`
	identity
}

func (e ToolCallStart) eventType() string { return e.Type }
func (e ToolCallStart) withIdentity(id identity) Event {
	e.identity = id
	return e
}
func newToolCallStart(id, name string) ToolCallStart {
	return ToolCallStart{Type: typeToolCallStart, ToolCallID: id, ToolCallName: name}
}

type ToolCallArgs struct {
	Type       string `json:"type"`
	ToolCallID string `json:"toolCallId"`
	Delta      string `json:"delta"`
	identity
}

func (e ToolCallArgs) eventType() string { return e.Type }
func (e ToolCallArgs) withIdentity(id identity) Event {
	e.identity = id
	return e
}
func newToolCallArgs(id, delta string) ToolCallArgs {
	return ToolCallArgs{Type: typeToolCallArgs, ToolCallID: id, Delta: delta}
}

type ToolCallEnd struct {
	Type       string `json:"type"`
	ToolCallID string `json:"toolCallId"`
	identity
}

func (e ToolCallEnd) eventType() string { return e.Type }
func (e ToolCallEnd) withIdentity(id identity) Event {
	e.identity = id
	return e
}
func newToolCallEnd(id string) ToolCallEnd {
	return ToolCallEnd{Type: typeToolCallEnd, ToolCallID: id}
}

type ToolCallResult struct {
	Type       string `json:"type"`
	MessageID  string `json:"messageId"`
	ToolCallID string `json:"toolCallId"`
	Content    string `json:"content"`
	Role       string `json:"role"`
	IsError    bool   `json:"isError,omitempty"`
	identity
}

func (e ToolCallResult) eventType() string { return e.Type }
func (e ToolCallResult) withIdentity(id identity) Event {
	e.identity = id
	return e
}
func newToolCallResult(msgID, toolCallID, content string, isError bool) ToolCallResult {
	return ToolCallResult{Type: typeToolCallResult, MessageID: msgID, ToolCallID: toolCallID, Content: content, Role: "tool", IsError: isError}
}

// --- Custom ---

// Custom is the AG-UI CUSTOM event: a named application-defined payload
// carried inside an otherwise standard stream. Gantry uses it for the
// gantry.subagent_done nested-completion signal (see mapper.go); clients
// that don't recognize the name ignore it.
type Custom struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value any    `json:"value"`
	identity
}

func (e Custom) eventType() string { return e.Type }
func (e Custom) withIdentity(id identity) Event {
	e.identity = id
	return e
}
func newCustom(name string, value any) Custom {
	return Custom{Type: typeCustom, Name: name, Value: value}
}

// subagentDoneName is the CUSTOM event name signaling that a NESTED
// sub-agent run finished — as opposed to RUN_FINISHED, which signals the
// single top-level AG-UI run finishing. See Mapper's EventDone handling.
const subagentDoneName = "gantry.subagent_done"

func newSubagentDone(id identity) Custom {
	c := newCustom(subagentDoneName, nil)
	c.identity = id
	return c
}

// eventsDroppedName is the CUSTOM event name signaling that a buffering
// wrapper (see gantry.NewBufferedSink) discarded events since the previous
// delivered one — i.e. this client's view of the run has a gap. See
// Mapper's generic gantry.Event.Dropped handling.
const eventsDroppedName = "gantry.events_dropped"

// eventsDroppedValue is the Custom.Value payload shape for eventsDroppedName.
type eventsDroppedValue struct {
	Count int `json:"count"`
}

func newEventsDropped(count int, id identity) Custom {
	c := newCustom(eventsDroppedName, eventsDroppedValue{Count: count})
	c.identity = id
	return c
}

// usageName is the CUSTOM event name carrying gantry's cumulative
// token/cost accounting for a run, translated from gantry.Event.Usage.
// Emitted only when usage changes since the last emission — see
// Mapper.usageDelta.
const usageName = "gantry.usage"

// usageValue is the Custom.Value payload shape for usageName.
type usageValue struct {
	InputTokens  int     `json:"inputTokens,omitempty"`
	OutputTokens int     `json:"outputTokens,omitempty"`
	Tokens       int     `json:"tokens,omitempty"` // InputTokens + OutputTokens, for clients that don't split them
	CostUSD      float64 `json:"costUsd,omitempty"`
}

func newUsage(inputTokens, outputTokens int, costUSD float64, id identity) Custom {
	c := newCustom(usageName, usageValue{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		Tokens:       inputTokens + outputTokens,
		CostUSD:      costUSD,
	})
	c.identity = id
	return c
}

// --- Raw ---

// Raw is the AG-UI RAW event: a passthrough for a provider frame gantry does
// not otherwise model (see the Anthropic adapter's default case in
// GenerateStream). Event carries the frame verbatim, undecoded.
type Raw struct {
	Type   string          `json:"type"`
	Event  json.RawMessage `json:"event"`
	Source string          `json:"source,omitempty"`
	identity
}

func (e Raw) eventType() string { return e.Type }
func (e Raw) withIdentity(id identity) Event {
	e.identity = id
	return e
}
func newRaw(event json.RawMessage, source string) Raw {
	return Raw{Type: typeRaw, Event: event, Source: source}
}

// --- Activity ---

// activityStepType is the ACTIVITY_SNAPSHOT/ACTIVITY_DELTA activityType value
// gantry uses for plan-step progress (see Mapper's EventPlanStepChanged
// handling). Named like the existing gantry.* CUSTOM event names.
const activityStepType = "gantry.plan_step"

// activityStepValue is the ACTIVITY_SNAPSHOT/ACTIVITY_DELTA content shape for
// a gantry plan step — a small client-facing projection of gantry.PlanStep,
// deliberately excluding its free-form Meta map. Output has no omitempty:
// the ACTIVITY_SNAPSHOT must always carry an "output" key (even "") so a
// later ACTIVITY_DELTA can validly "replace" it per RFC 6902 — a "replace"
// against a path absent from the last snapshot is undefined behavior for a
// JSON Patch client.
type activityStepValue struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Output      string `json:"output"`
}

// jsonPatchOp is one RFC 6902 JSON Patch operation, as required by
// ActivityDelta.Patch.
type jsonPatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

type ActivitySnapshot struct {
	Type         string `json:"type"`
	MessageID    string `json:"messageId"`
	ActivityType string `json:"activityType"`
	Content      any    `json:"content"`
	identity
}

func (e ActivitySnapshot) eventType() string { return e.Type }
func (e ActivitySnapshot) withIdentity(id identity) Event {
	e.identity = id
	return e
}
func newActivitySnapshot(msgID string, content activityStepValue) ActivitySnapshot {
	return ActivitySnapshot{Type: typeActivitySnapshot, MessageID: msgID, ActivityType: activityStepType, Content: content}
}

type ActivityDelta struct {
	Type         string        `json:"type"`
	MessageID    string        `json:"messageId"`
	ActivityType string        `json:"activityType"`
	Patch        []jsonPatchOp `json:"patch"`
	identity
}

func (e ActivityDelta) eventType() string { return e.Type }
func (e ActivityDelta) withIdentity(id identity) Event {
	e.identity = id
	return e
}
func newActivityDelta(msgID string, patch []jsonPatchOp) ActivityDelta {
	return ActivityDelta{Type: typeActivityDelta, MessageID: msgID, ActivityType: activityStepType, Patch: patch}
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

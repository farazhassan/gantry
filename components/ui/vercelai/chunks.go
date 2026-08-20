// Package vercelai exposes a Gantry agent over the Vercel AI SDK v5 "UI
// Message Stream" protocol. See doc.go for the full package overview and
// scope; this file defines the wire-level chunk DTOs and SSE framing.
package vercelai

import (
	"encoding/json"
	"fmt"
	"io"
)

// Chunk is one UI Message Stream chunk. Concrete types marshal to the exact
// wire JSON: a kebab-case "type" discriminator plus camelCase fields.
type Chunk interface {
	// chunkType returns the wire "type" value; it also keeps Chunk closed
	// to this package.
	chunkType() string
}

// Wire "type" discriminators, verified against the SDK's own TypeScript
// source (packages/ai/src/ui-message-stream/ui-message-chunks.ts in
// vercel/ai), not just prose docs.
const (
	typeStart  = "start"
	typeFinish = "finish"

	typeTextStart = "text-start"
	typeTextDelta = "text-delta"
	typeTextEnd   = "text-end"

	typeReasoningStart = "reasoning-start"
	typeReasoningDelta = "reasoning-delta"
	typeReasoningEnd   = "reasoning-end"

	typeToolInputStart      = "tool-input-start"
	typeToolInputAvailable  = "tool-input-available"
	typeToolOutputAvailable = "tool-output-available"
	typeToolOutputError     = "tool-output-error"

	typeStartStep  = "start-step"
	typeFinishStep = "finish-step"

	typeErrorChunk = "error"

	// dataTypePrefix is prepended to the gantry-specific data-part name
	// (e.g. "gantry-usage") to form the wire "type" value ("data-gantry-usage")
	// — the protocol's escape hatch for app-specific data, used here the
	// same way agui uses its CUSTOM event for gantry-specific augmentation.
	dataTypePrefix = "data-"
)

// --- Message lifecycle ---

type Start struct {
	Type      string `json:"type"`
	MessageID string `json:"messageId,omitempty"`
}

func (Start) chunkType() string       { return typeStart }
func newStart(messageID string) Start { return Start{Type: typeStart, MessageID: messageID} }

type Finish struct {
	Type string `json:"type"`
}

func (Finish) chunkType() string { return typeFinish }
func newFinish() Finish          { return Finish{Type: typeFinish} }

// --- Text ---

type TextStart struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func (TextStart) chunkType() string    { return typeTextStart }
func newTextStart(id string) TextStart { return TextStart{Type: typeTextStart, ID: id} }

type TextDelta struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Delta string `json:"delta"`
}

func (TextDelta) chunkType() string { return typeTextDelta }
func newTextDelta(id, delta string) TextDelta {
	return TextDelta{Type: typeTextDelta, ID: id, Delta: delta}
}

type TextEnd struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func (TextEnd) chunkType() string  { return typeTextEnd }
func newTextEnd(id string) TextEnd { return TextEnd{Type: typeTextEnd, ID: id} }

// --- Reasoning (mirrors Text* exactly — one bracket level, unlike agui's
// nested REASONING_START/REASONING_MESSAGE_START pair) ---

type ReasoningStart struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func (ReasoningStart) chunkType() string { return typeReasoningStart }
func newReasoningStart(id string) ReasoningStart {
	return ReasoningStart{Type: typeReasoningStart, ID: id}
}

type ReasoningDelta struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Delta string `json:"delta"`
}

func (ReasoningDelta) chunkType() string { return typeReasoningDelta }
func newReasoningDelta(id, delta string) ReasoningDelta {
	return ReasoningDelta{Type: typeReasoningDelta, ID: id, Delta: delta}
}

type ReasoningEnd struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func (ReasoningEnd) chunkType() string { return typeReasoningEnd }
func newReasoningEnd(id string) ReasoningEnd {
	return ReasoningEnd{Type: typeReasoningEnd, ID: id}
}

// --- Tool calls ---

// ToolInputStart and ToolInputAvailable are always emitted with
// Dynamic=true: gantry doesn't know a tool's name until the agent is
// configured at runtime, so every Gantry tool call maps to the SDK's
// "dynamic-tool" concept rather than one of its compile-time-typed
// "tool-<name>" parts. gantry doesn't stream partial tool-call arguments
// (ToolCall.Input always arrives complete), so there's no
// "tool-input-delta" — Start and Available are emitted back to back, the
// same simplification agui made for TOOL_CALL_ARGS.
type ToolInputStart struct {
	Type       string `json:"type"`
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	Dynamic    bool   `json:"dynamic"`
}

func (ToolInputStart) chunkType() string { return typeToolInputStart }
func newToolInputStart(id, name string) ToolInputStart {
	return ToolInputStart{Type: typeToolInputStart, ToolCallID: id, ToolName: name, Dynamic: true}
}

type ToolInputAvailable struct {
	Type       string          `json:"type"`
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Input      json.RawMessage `json:"input"`
	Dynamic    bool            `json:"dynamic"`
}

func (ToolInputAvailable) chunkType() string { return typeToolInputAvailable }

// newToolInputAvailable normalizes a nil/empty input to "{}": Input is a
// required field on the wire (the SDK types it as "unknown", not
// "unknown | undefined"), and gantry.ToolCall.Input is nil for a
// zero-argument tool call.
func newToolInputAvailable(id, name string, input json.RawMessage) ToolInputAvailable {
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	return ToolInputAvailable{Type: typeToolInputAvailable, ToolCallID: id, ToolName: name, Input: input, Dynamic: true}
}

// ToolOutputAvailable.Output is typed as a Go string, not json.RawMessage:
// gantry.ToolResult.Content is always a plain string, and a JSON string is
// itself a valid value for the wire's "output: unknown" field — no
// unwrapping or re-parsing needed.
type ToolOutputAvailable struct {
	Type       string `json:"type"`
	ToolCallID string `json:"toolCallId"`
	Output     string `json:"output"`
}

func (ToolOutputAvailable) chunkType() string { return typeToolOutputAvailable }
func newToolOutputAvailable(id, output string) ToolOutputAvailable {
	return ToolOutputAvailable{Type: typeToolOutputAvailable, ToolCallID: id, Output: output}
}

type ToolOutputError struct {
	Type       string `json:"type"`
	ToolCallID string `json:"toolCallId"`
	ErrorText  string `json:"errorText"`
}

func (ToolOutputError) chunkType() string { return typeToolOutputError }
func newToolOutputError(id, errText string) ToolOutputError {
	return ToolOutputError{Type: typeToolOutputError, ToolCallID: id, ErrorText: errText}
}

// --- Steps ---

type StartStep struct {
	Type string `json:"type"`
}

func (StartStep) chunkType() string { return typeStartStep }
func newStartStep() StartStep       { return StartStep{Type: typeStartStep} }

type FinishStep struct {
	Type string `json:"type"`
}

func (FinishStep) chunkType() string { return typeFinishStep }
func newFinishStep() FinishStep      { return FinishStep{Type: typeFinishStep} }

// --- Error ---

type ErrorChunk struct {
	Type      string `json:"type"`
	ErrorText string `json:"errorText"`
}

func (ErrorChunk) chunkType() string { return typeErrorChunk }
func newErrorChunk(msg string) ErrorChunk {
	return ErrorChunk{Type: typeErrorChunk, ErrorText: msg}
}

// --- Data (gantry-specific escape hatch) ---

// DataChunk is a "data-<name>" chunk: the protocol's own mechanism for
// app-specific data riding alongside the standard parts, used here the way
// agui uses its CUSTOM event — for gantry-specific augmentation (usage,
// dropped-event counts, plan-step activity, raw provider frames) that has
// no standard chunk type of its own. ID, when set, lets a client that
// tracks data parts by id treat repeated chunks with the same id as
// updates to the same logical data part (used for plan-step activity,
// which changes over time under a stable step id).
type DataChunk struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
	Data any    `json:"data"`
}

func (c DataChunk) chunkType() string { return c.Type }
func newDataChunk(name, id string, data any) DataChunk {
	return DataChunk{Type: dataTypePrefix + name, ID: id, Data: data}
}

// usageValue is the data-gantry-usage payload shape, mirroring agui's
// gantry.usage CUSTOM event value.
type usageValue struct {
	InputTokens  int     `json:"inputTokens,omitempty"`
	OutputTokens int     `json:"outputTokens,omitempty"`
	Tokens       int     `json:"tokens,omitempty"` // InputTokens + OutputTokens, for clients that don't split them
	CostUSD      float64 `json:"costUsd,omitempty"`
}

// eventsDroppedValue is the data-gantry-events-dropped payload shape.
type eventsDroppedValue struct {
	Count int `json:"count"`
}

// planStepValue is the data-gantry-plan-step payload shape — a small
// client-facing projection of gantry.PlanStep, deliberately excluding its
// free-form Meta map. Output has no omitempty so a client always sees the
// key present, even as "".
type planStepValue struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Output      string `json:"output"`
}

// rawValue is the data-gantry-raw payload shape, mirroring agui's RAW event.
type rawValue struct {
	Event  json.RawMessage `json:"event"`
	Source string          `json:"source,omitempty"`
}

// --- SSE framing ---

// WriteSSE marshals c and writes it as one Server-Sent Events frame:
// "data: <json>\n\n". The chunk discriminator lives in the JSON "type"
// field, so no SSE "event:" line is emitted.
func WriteSSE(w io.Writer, c Chunk) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", b)
	return err
}

// writeDone writes the terminal "data: [DONE]\n\n" line the AI SDK client
// uses to detect stream end — verified against the SDK's own
// JsonToSseTransformStream, which writes this unconditionally in flush(),
// regardless of whether the stream ended in "finish" or "error".
func writeDone(w io.Writer) error {
	_, err := io.WriteString(w, "data: [DONE]\n\n")
	return err
}

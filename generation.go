package gantry

import (
	"context"
	"encoding/json"
)

// traceMessage is the JSON shape recorded for each conversation message on a
// generation span's input/output. Role/content plus optional tool-call linkage
// let Langfuse (and other viewers) render the exchange as a chat transcript.
type traceMessage struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	ToolCalls  []traceToolCall `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type traceToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func toTraceMessage(m Message) traceMessage {
	tm := traceMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
	for _, tc := range m.ToolCalls {
		tm.ToolCalls = append(tm.ToolCalls, traceToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Input})
	}
	return tm
}

// marshalMessages serializes the system prompt (as a leading system message,
// when non-empty) plus the transcript into a JSON array of traceMessage. On a
// marshal error it returns "" so tracing never breaks a run.
func marshalMessages(system string, msgs []Message) string {
	out := make([]traceMessage, 0, len(msgs)+1)
	if system != "" {
		out = append(out, traceMessage{Role: string(RoleSystem), Content: system})
	}
	for _, m := range msgs {
		out = append(out, toTraceMessage(m))
	}
	b, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(b)
}

// marshalResponse serializes an LLM reply as a single assistant traceMessage.
func marshalResponse(resp LLMResponse) string {
	b, err := json.Marshal(toTraceMessage(Message{
		Role:      RoleAssistant,
		Content:   resp.Content,
		ToolCalls: resp.ToolCalls,
	}))
	if err != nil {
		return ""
	}
	return string(b)
}

// generation wraps an optional tracer span for one LLM call. A zero generation
// (no tracer in ctx) makes end a no-op.
type generation struct{ span Span }

// startGeneration opens a "model.call" child span under the current span when a
// Tracer is present in ctx, recording the prompt as structured input and
// marking the span as a generation observation. The returned context carries the
// new span so any deeper spans nest correctly.
func startGeneration(ctx context.Context, req LLMRequest) (context.Context, generation) {
	tr := tracerFrom(ctx)
	if tr == nil {
		return ctx, generation{}
	}
	ctx, span := tr.StartSpan(ctx, "model.call")
	span.SetAttr("observation.type", "generation")
	span.SetAttr("input", marshalMessages(req.System, req.Messages))
	return ctx, generation{span: span}
}

// end records the completion, token usage, and model on the generation span,
// then ends it. A non-nil error is passed through to Span.End so the tracer can
// mark the observation failed; output/usage are only recorded on success.
//
// Attribute keys here (observation.type, input, output, usage_in, usage_out,
// model) are deliberately exporter-neutral: gantry does not bind to any vendor's
// semantic conventions. Each tracer/exporter maps them to its own model — e.g.
// an OTLP/Langfuse exporter maps model -> gen_ai.request.model and usage_in/out
// -> gen_ai.usage.{input,output}_tokens.
func (g generation) end(resp LLMResponse, err error) {
	if g.span == nil {
		return
	}
	if err == nil {
		g.span.SetAttr("output", marshalResponse(resp))
		g.span.SetAttr("usage_in", resp.Usage.InputTokens)
		g.span.SetAttr("usage_out", resp.Usage.OutputTokens)
		if resp.Model != "" {
			g.span.SetAttr("model", resp.Model)
		}
	}
	g.span.End(err)
}

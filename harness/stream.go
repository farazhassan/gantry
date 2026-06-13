package harness

import "context"

// StreamChunk is one incremental update from a streaming LLM call. A chunk
// carries a text delta, and/or the terminal StopReason + Usage on the final
// chunk. Fields are omitempty so chunks serialize compactly.
type StreamChunk struct {
	TextDelta  string     `json:"text_delta,omitempty"`
	StopReason StopReason `json:"stop_reason,omitempty"`
	Usage      *Usage     `json:"usage,omitempty"`
}

// StreamingLLMClient is an OPTIONAL extension of LLMClient. Adapters that can
// stream implement it; the agent detects support via a type assertion, so a
// plain LLMClient continues to work unchanged.
//
// GenerateStream invokes yield once per chunk as content arrives. If yield
// returns an error, GenerateStream must stop and return that error. It must
// still return the fully-aggregated LLMResponse on success so callers that
// also want the whole response (transcript append, usage accounting) get it
// without re-assembling deltas.
type StreamingLLMClient interface {
	LLMClient
	GenerateStream(ctx context.Context, req LLMRequest, yield func(StreamChunk) error) (LLMResponse, error)
}

// EventType discriminates Event for JSON consumers (e.g. a browser switch).
type EventType string

const (
	EventPhaseStart EventType = "phase_start"
	EventPhaseEnd   EventType = "phase_end"
	EventTextDelta  EventType = "text_delta"
	EventToolCall   EventType = "tool_call"
	EventToolResult EventType = "tool_result"
	EventDone       EventType = "done"
)

// Event is a single whole-run observation emitted by RunStream. It is
// JSON-serializable with a Type discriminator so a web server can forward it
// over SSE/WebSocket verbatim. Only the fields relevant to Type are set; the
// rest are omitted.
type Event struct {
	Type        EventType   `json:"type"`
	Iteration   int         `json:"iteration"`
	Phase       Phase       `json:"phase,omitempty"`
	TextDelta   string      `json:"text_delta,omitempty"`
	ToolCall    *ToolCall   `json:"tool_call,omitempty"`
	ToolResult  *ToolResult `json:"tool_result,omitempty"`
	DoneReason  DoneReason  `json:"done_reason,omitempty"`
	FinalOutput string      `json:"final_output,omitempty"`
}

// EventSink receives run Events. Returning an error aborts the run and the
// error propagates out of RunStream (wrapped with the partial trace). The sink
// is called synchronously on the run goroutine; fan-out consumers (WebSocket,
// multiple subscribers) should hand off to their own goroutine and return
// quickly to avoid blocking the loop.
type EventSink func(Event) error

// sinkKey is the context key under which the active EventSink is stored.
type sinkKey struct{}

func withSink(ctx context.Context, s EventSink) context.Context {
	return context.WithValue(ctx, sinkKey{}, s)
}

func sinkFrom(ctx context.Context) EventSink {
	s, _ := ctx.Value(sinkKey{}).(EventSink)
	return s
}

// emit sends ev to the sink in ctx, if any. With no sink it is a no-op, which
// is what makes the shared run() loop free for plain Run.
func emit(ctx context.Context, ev Event) error {
	if s := sinkFrom(ctx); s != nil {
		return s(ev)
	}
	return nil
}

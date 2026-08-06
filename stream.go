package gantry

import (
	"context"
	"sync/atomic"
)

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
// rest are omitted. Note on ordering: EventToolCall and EventToolResult events
// are emitted after the phase_end event of the phase that produced them,
// because they are derived from State and only become available once that
// phase's handler has returned. The identity fields (RunID, SessionID, TaskID,
// Agent, ParentRunID, ParentToolCallID) are stamped by the run loop on every
// emitted event and are empty when unknown.
type Event struct {
	Type        EventType   `json:"type"`
	Iteration   int         `json:"iteration"`
	Phase       Phase       `json:"phase,omitempty"`
	TextDelta   string      `json:"text_delta,omitempty"`
	ToolCall    *ToolCall   `json:"tool_call,omitempty"`
	ToolResult  *ToolResult `json:"tool_result,omitempty"`
	DoneReason  DoneReason  `json:"done_reason,omitempty"`
	FinalOutput string      `json:"final_output,omitempty"`

	// Usage is a snapshot of the run's cumulative token/cost accounting as of
	// this event (see State.Usage), attached to phase_end and done events. A
	// nil Usage means the snapshot wasn't taken for this event, not that
	// usage is zero — check the phase_end/done events for the latest value.
	Usage *Usage `json:"usage,omitempty"`

	// Identity: which run, session, task, and agent produced this event.
	// RunID is minted per run; SessionID/TaskID come from State.Meta
	// (MetaSessionID / MetaTaskID) when a task driver seeded them; Agent is
	// the WithName identity. All empty when unknown.
	RunID     string `json:"run_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	Agent     string `json:"agent,omitempty"`

	// ParentRunID/ParentToolCallID identify the run and specific ToolCall
	// that spawned this run as a nested sub-agent (see WithParentLink).
	// Both empty for a top-level run.
	ParentRunID      string `json:"parent_run_id,omitempty"`
	ParentToolCallID string `json:"parent_tool_call_id,omitempty"`

	// Dropped counts events discarded by a buffering wrapper (see
	// NewBufferedSink) since the previous delivered event; zero on direct,
	// unbuffered streams. A plain int so Event stays comparable.
	Dropped int `json:"dropped,omitempty"`
}

// EventSink receives run Events. Returning an error aborts the run and the
// error propagates out of RunStream (wrapped with the partial trace). The sink
// is called synchronously on the run goroutine; fan-out consumers (WebSocket,
// multiple subscribers) should hand off to their own goroutine and return
// quickly to avoid blocking the loop.
type EventSink func(Event) error

// sinkKey is the context key under which the active EventSink is stored.
type sinkKey struct{}

// WithSink returns a ctx carrying sink as the ambient EventSink. The run loop
// installs the RunStream sink through here; callers embedding gantry (custom
// runners, tests) may install their own. A nil sink behaves like WithoutSink.
func WithSink(ctx context.Context, s EventSink) context.Context {
	return context.WithValue(ctx, sinkKey{}, s)
}

// WithoutSink returns a ctx whose ambient EventSink is shadowed: SinkFrom
// reports no sink and emit becomes a no-op downstream of it. Use it when
// running a nested agent from inside a streaming parent run (e.g. a delegate
// tool) so the child's events do not interleave into the parent's stream.
func WithoutSink(ctx context.Context) context.Context {
	return context.WithValue(ctx, sinkKey{}, EventSink(nil))
}

// SinkFrom returns the ambient EventSink and whether one is installed. A ctx
// shadowed by WithoutSink (or carrying a nil sink) reports (nil, false).
func SinkFrom(ctx context.Context) (EventSink, bool) {
	s, _ := ctx.Value(sinkKey{}).(EventSink)
	return s, s != nil
}

// emit sends ev to the sink in ctx, if any, first stamping the run's ambient
// identity (RunID/SessionID/TaskID/Agent/ParentRunID/ParentToolCallID) onto
// it. The ambient identity is authoritative: internal emit sites construct
// events with empty identity and rely on this single chokepoint. With no
// sink emit is a no-op, which is what makes the shared run() loop free for
// plain Run.
func emit(ctx context.Context, ev Event) error {
	s, ok := SinkFrom(ctx)
	if !ok {
		return nil
	}
	if id, ok := identityFrom(ctx); ok {
		ev.RunID = id.runID
		ev.SessionID = id.sessionID
		ev.TaskID = id.taskID
		ev.Agent = id.agent
		ev.ParentRunID = id.parentRunID
		ev.ParentToolCallID = id.parentToolCallID
	}
	return s(ev)
}

// NewBufferedSink wraps sink so event production is decoupled from consumption:
// the returned EventSink enqueues onto a bounded buffer and NEVER blocks, while
// a single consumer goroutine delivers to the wrapped sink in FIFO order. Use it
// wherever the emitting goroutine must not stall on a slow consumer — e.g. a
// task Driver's WithEventSink, where the raw sink runs synchronously on the run
// goroutine and would otherwise stall the Dispatcher.
//
// When the buffer is full the OLDEST queued event is dropped to admit the
// newest (fresh progress beats a stale backlog; the durable record lives in the
// stores, not the stream). The number of events dropped since the previous
// delivery is surfaced on the next delivered event's Dropped field.
//
// size is the buffer capacity; size <= 0 defaults to 64. The returned stop
// function drains remaining buffered events, waits for the consumer goroutine
// to exit, and is idempotent; events sent after stop returns are silently
// discarded. Because delivery is asynchronous, an error returned by the wrapped
// sink cannot abort the (long-finished) emitting run and is discarded. A nil
// sink panics.
func NewBufferedSink(sink EventSink, size int) (EventSink, func()) {
	if sink == nil {
		panic("gantry: NewBufferedSink requires a non-nil sink")
	}
	if size <= 0 {
		size = 64
	}
	b := &bufferedSink{
		ch:   make(chan Event, size),
		sink: sink,
		quit: make(chan struct{}),
		done: make(chan struct{}),
	}
	go b.consume()
	return b.send, b.stop
}

// bufferedSink is the state behind NewBufferedSink. The channel is never
// closed (producers may race stop), so every send/receive is non-blocking and
// panic-free by construction.
type bufferedSink struct {
	ch      chan Event
	sink    EventSink
	dropped atomic.Int64
	stopped atomic.Bool
	quit    chan struct{}
	done    chan struct{}
}

// send is the producer side: enqueue without ever blocking, evicting the
// oldest queued event when full. Always returns nil — a buffered producer has
// nothing to abort on.
func (b *bufferedSink) send(ev Event) error {
	if b.stopped.Load() {
		return nil // late event after stop: discard silently
	}
	select {
	case b.ch <- ev:
		return nil
	default:
	}
	// Full: evict the oldest queued event to make room for the newest.
	select {
	case <-b.ch:
		b.dropped.Add(1)
	default:
	}
	select {
	case b.ch <- ev:
	default:
		// Lost the refill race to another producer: the incoming event is the
		// one dropped instead.
		b.dropped.Add(1)
	}
	return nil
}

// consume is the single consumer goroutine: deliver until quit, then drain
// whatever is left and exit.
func (b *bufferedSink) consume() {
	defer close(b.done)
	for {
		select {
		case ev := <-b.ch:
			b.deliver(ev)
		case <-b.quit:
			for {
				select {
				case ev := <-b.ch:
					b.deliver(ev)
				default:
					return
				}
			}
		}
	}
}

// deliver stamps any pending drop count onto ev and hands it to the wrapped
// sink. Drops can only occur while the buffer is full, so at least one queued
// event always remains to carry the count — it is never silently lost.
func (b *bufferedSink) deliver(ev Event) {
	if n := b.dropped.Swap(0); n > 0 {
		ev.Dropped = int(n)
	}
	_ = b.sink(ev) // async delivery: nothing left to abort; error discarded
}

// stop signals the consumer to drain and exit, then waits for it. Idempotent.
func (b *bufferedSink) stop() {
	if b.stopped.CompareAndSwap(false, true) {
		close(b.quit)
	}
	<-b.done
}

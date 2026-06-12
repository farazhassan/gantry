package harness

import (
	"context"
	"sync"
	"time"
)

// EventKind distinguishes span boundaries from point-in-time events.
type EventKind int

const (
	KindSpanStart EventKind = iota
	KindSpanEnd
	KindEvent
)

// TraceEvent is one row in the trace. Spans appear as a paired
// KindSpanStart and KindSpanEnd with the same SpanID.
type TraceEvent struct {
	SpanID    string
	ParentID  string
	Name      string
	Kind      EventKind
	StartTime time.Time
	EndTime   time.Time // populated on KindSpanEnd
	Duration  time.Duration
	Attrs     map[string]any
	Err       error
}

// Trace is the agent-scoped collector of trace events. Safe for concurrent
// recording from tool-execution goroutines.
type Trace struct {
	mu     sync.Mutex
	events []TraceEvent
}

// NewTrace returns an empty Trace.
func NewTrace() *Trace {
	return &Trace{}
}

// Record appends an event. Safe for concurrent use.
func (t *Trace) Record(ev TraceEvent) {
	t.mu.Lock()
	t.events = append(t.events, ev)
	t.mu.Unlock()
}

// Snapshot returns a copy of the events recorded so far.
func (t *Trace) Snapshot() []TraceEvent {
	t.mu.Lock()
	out := make([]TraceEvent, len(t.events))
	copy(out, t.events)
	t.mu.Unlock()
	return out
}

// Tracer creates spans. The agent calls StartSpan at the start of each
// phase; built-in middleware can open nested spans.
type Tracer interface {
	StartSpan(ctx context.Context, name string) (context.Context, Span)
}

// Span is the active span returned by Tracer.StartSpan.
type Span interface {
	SetAttr(k string, v any)
	RecordEvent(name string, attrs map[string]any)
	End(err error)
}

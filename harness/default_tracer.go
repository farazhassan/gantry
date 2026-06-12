package harness

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

// defaultTracer is the in-memory tracer used by default. It writes spans
// and events to a shared *Trace.
type defaultTracer struct {
	trace *Trace
}

// NewDefaultTracer returns a Tracer that records into the supplied Trace.
func NewDefaultTracer(t *Trace) Tracer {
	return &defaultTracer{trace: t}
}

type spanCtxKey struct{}

func currentSpanID(ctx context.Context) string {
	if v, ok := ctx.Value(spanCtxKey{}).(string); ok {
		return v
	}
	return ""
}

func (d *defaultTracer) StartSpan(ctx context.Context, name string) (context.Context, Span) {
	id := newID()
	parent := currentSpanID(ctx)
	start := time.Now()
	d.trace.Record(TraceEvent{
		SpanID:    id,
		ParentID:  parent,
		Name:      name,
		Kind:      KindSpanStart,
		StartTime: start,
	})
	s := &defaultSpan{
		trace:    d.trace,
		id:       id,
		parentID: parent,
		name:     name,
		start:    start,
		attrs:    map[string]any{},
	}
	return context.WithValue(ctx, spanCtxKey{}, id), s
}

type defaultSpan struct {
	trace    *Trace
	id       string
	parentID string
	name     string
	start    time.Time
	attrs    map[string]any
}

func (s *defaultSpan) SetAttr(k string, v any) {
	s.attrs[k] = v
}

func (s *defaultSpan) RecordEvent(name string, attrs map[string]any) {
	s.trace.Record(TraceEvent{
		SpanID:    s.id,
		ParentID:  s.parentID,
		Name:      name,
		Kind:      KindEvent,
		StartTime: time.Now(),
		Attrs:     attrs,
	})
}

func (s *defaultSpan) End(err error) {
	end := time.Now()
	s.trace.Record(TraceEvent{
		SpanID:    s.id,
		ParentID:  s.parentID,
		Name:      s.name,
		Kind:      KindSpanEnd,
		StartTime: s.start,
		EndTime:   end,
		Duration:  end.Sub(s.start),
		Attrs:     s.attrs,
		Err:       err,
	})
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// fall back to a deterministic-but-unique id if rand is unavailable
		return time.Now().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(b[:])
}

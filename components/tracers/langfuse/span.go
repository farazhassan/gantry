package langfuse

import (
	"context"
	"time"

	"github.com/farazhassan/gantry/harness"
)

type spanCtxKey struct{}

func spanIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(spanCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// StartSpan opens a span. A parentless span opens a new Langfuse trace; nested
// spans become observations under it. Gantry's harness wraps each agent run in
// a single top-level "run" span, so one run maps to one Langfuse trace. The
// returned context carries this span's id so descendant StartSpan calls parent
// themselves correctly.
func (c *Client) StartSpan(ctx context.Context, name string) (context.Context, harness.Span) {
	id := newID()
	parent := spanIDFromContext(ctx)
	traceID := c.registerTrace(id, parent)
	s := &span{
		client:   c,
		traceID:  traceID,
		spanID:   id,
		parentID: parent,
		name:     name,
		start:    time.Now(),
		attrs:    map[string]any{},
	}
	return context.WithValue(ctx, spanCtxKey{}, id), s
}

// registerTrace resolves and records the trace id for spanID. A parentless span
// is its own trace root; a child inherits its parent's trace id. Registration
// happens at StartSpan (parent id is known at start), so trace-id resolution is
// independent of the order spans End in. An orphan (unknown parent) falls back
// to its own id as the trace id so its observation stays deliverable.
func (c *Client) registerTrace(spanID, parentID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	traceID := spanID
	if parentID != "" {
		if t, ok := c.traceIDs[parentID]; ok {
			traceID = t
		}
	}
	c.traceIDs[spanID] = traceID
	return traceID
}

// unregister removes a span's trace-id entry once the span ends. Safe because
// Gantry spans are strictly nested: a child always starts before its parent
// ends, so no later StartSpan will look this entry up.
func (c *Client) unregister(spanID string) {
	c.mu.Lock()
	delete(c.traceIDs, spanID)
	c.mu.Unlock()
}

type span struct {
	client   *Client
	traceID  string
	spanID   string
	parentID string
	name     string
	start    time.Time
	attrs    map[string]any
}

func (s *span) SetAttr(k string, v any) { s.attrs[k] = v }

func (s *span) RecordEvent(name string, attrs map[string]any) {
	s.client.enqueue(eventCreateItem(s.traceID, s.spanID, name, time.Now(), attrs))
}

func (s *span) End(err error) {
	end := time.Now()
	if s.parentID == "" {
		s.client.enqueue(traceCreateItem(s.traceID, s.name, s.start))
	}
	s.client.enqueue(spanCreateItem(s.traceID, s.spanID, s.parentID, s.name, s.start, end, s.attrs, err))
	s.client.unregister(s.spanID)
}

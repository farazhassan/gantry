package langfuse

import (
	"context"
	"encoding/json"
	"time"

	"github.com/farazhassan/gantry"
)

type spanCtxKey struct{}

func spanIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(spanCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// StartSpan opens a span. A parentless span opens a new Langfuse trace; nested
// spans become observations under it. Gantry wraps each agent run in
// a single top-level "run" span, so one run maps to one Langfuse trace. The
// returned context carries this span's id so descendant StartSpan calls parent
// themselves correctly.
func (c *Client) StartSpan(ctx context.Context, name string) (context.Context, gantry.Span) {
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

	// Partition attrs: reserved content keys get native treatment (eagerly
	// marshaled + redacted here, on this goroutine, where state is stable);
	// everything else stays as metadata exactly as before.
	var input, output, state, usage json.RawMessage
	var hasInput, hasOutput, hasState, hasUsage bool
	obsType := ""
	var leftover map[string]any // allocated lazily on the first non-reserved attr
	for k, v := range s.attrs {
		switch k {
		case gantry.AttrObservationType:
			if str, ok := v.(string); ok {
				obsType = str
			}
		case gantry.AttrInput:
			input, hasInput = s.client.redact(k, v)
		case gantry.AttrOutput:
			output, hasOutput = s.client.redact(k, v)
		case gantry.AttrState:
			state, hasState = s.client.redact(k, v)
		case gantry.AttrUsage:
			usage, hasUsage = s.client.redact(k, v)
		default:
			if leftover == nil {
				leftover = map[string]any{}
			}
			leftover[k] = v
		}
	}

	if s.parentID == "" {
		trace := traceCreateItem(s.traceID, s.name, s.start)
		if hasInput {
			trace.Body["input"] = input
		}
		if hasOutput {
			trace.Body["output"] = output
		}
		if hasState {
			trace.Body["metadata"] = map[string]any{"state": state}
		}
		s.client.enqueue(trace)
	}

	if obsType == gantry.ObservationGeneration {
		var gi, gOut, gu json.RawMessage
		if hasInput {
			gi = input
		}
		if hasOutput {
			gOut = output
		}
		if hasUsage {
			gu = usage
		}
		s.client.enqueue(generationCreateItem(s.traceID, s.spanID, s.parentID, s.name, s.start, end, gi, gOut, gu, leftover, err))
	} else {
		s.client.enqueue(spanCreateItem(s.traceID, s.spanID, s.parentID, s.name, s.start, end, leftover, err))
	}
	s.client.unregister(s.spanID)
}

package langfuse

import (
	"context"
	"errors"
	"testing"
)

// byType returns captured items whose Type matches.
func byType(items []ingestionItem, typ string) []ingestionItem {
	var out []ingestionItem
	for _, it := range items {
		if it.Type == typ {
			out = append(out, it)
		}
	}
	return out
}

func TestRootSpanEmitsTraceAndObservation(t *testing.T) {
	c, cap := newServerClient(t)
	_, span := c.StartSpan(context.Background(), "phase:plan")
	span.SetAttr("iteration", 1)
	span.End(nil)
	if err := c.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	items := cap.items()
	traces := byType(items, "trace-create")
	spans := byType(items, "span-create")
	if len(traces) != 1 {
		t.Fatalf("got %d trace-create, want 1", len(traces))
	}
	if len(spans) != 1 {
		t.Fatalf("got %d span-create, want 1", len(spans))
	}
	traceID := traces[0].Body["id"]
	if spans[0].Body["traceId"] != traceID {
		t.Fatalf("span traceId %v != trace id %v", spans[0].Body["traceId"], traceID)
	}
	if spans[0].Body["id"] != traceID {
		t.Fatalf("root span id should equal trace id; got %v vs %v", spans[0].Body["id"], traceID)
	}
	md, _ := spans[0].Body["metadata"].(map[string]any)
	// The capture decodes JSON into map[string]any, so numbers arrive as
	// float64; compare numerically rather than by Go type.
	if iter, ok := md["iteration"].(float64); !ok || iter != 1 {
		t.Fatalf("metadata iteration = %v, want 1", md["iteration"])
	}
}

func TestNestedSpanSharesTraceAndSetsParent(t *testing.T) {
	c, cap := newServerClient(t)
	ctx, outer := c.StartSpan(context.Background(), "outer")
	_, inner := c.StartSpan(ctx, "inner")
	inner.End(nil) // inner ends before outer
	outer.End(nil)
	if err := c.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	items := cap.items()
	traces := byType(items, "trace-create")
	if len(traces) != 1 {
		t.Fatalf("got %d trace-create, want exactly 1 for the run", len(traces))
	}
	traceID := traces[0].Body["id"]
	rootID := traceID // root span id == trace id

	spans := byType(items, "span-create")
	if len(spans) != 2 {
		t.Fatalf("got %d span-create, want 2", len(spans))
	}
	for _, s := range spans {
		if s.Body["traceId"] != traceID {
			t.Fatalf("span %v has traceId %v, want %v", s.Body["id"], s.Body["traceId"], traceID)
		}
		if s.Body["name"] == "inner" {
			if s.Body["parentObservationId"] != rootID {
				t.Fatalf("inner parentObservationId = %v, want %v", s.Body["parentObservationId"], rootID)
			}
		}
	}
}

func TestRecordEventEmitsEventObservation(t *testing.T) {
	c, cap := newServerClient(t)
	_, span := c.StartSpan(context.Background(), "phase:act")
	span.RecordEvent("tool_call", map[string]any{"tool": "search"})
	span.End(nil)
	if err := c.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	events := byType(cap.items(), "event-create")
	if len(events) != 1 {
		t.Fatalf("got %d event-create, want 1", len(events))
	}
	if events[0].Body["name"] != "tool_call" {
		t.Fatalf("event name = %v, want tool_call", events[0].Body["name"])
	}
	md, _ := events[0].Body["metadata"].(map[string]any)
	if md["tool"] != "search" {
		t.Fatalf("event metadata = %v, want tool=search", events[0].Body["metadata"])
	}
}

func TestEndWithErrorMarksObservation(t *testing.T) {
	c, cap := newServerClient(t)
	_, span := c.StartSpan(context.Background(), "phase:act")
	span.End(errors.New("boom"))
	if err := c.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	spans := byType(cap.items(), "span-create")
	if len(spans) != 1 || spans[0].Body["level"] != "ERROR" || spans[0].Body["statusMessage"] != "boom" {
		t.Fatalf("error mapping wrong: %v", spans)
	}
}

func TestRunPatternProducesSingleTrace(t *testing.T) {
	c, cap := newServerClient(t)
	// Mimic the gantry: one "run" span, phases nested under its context,
	// inner phases ending before the run span.
	ctx, runSpan := c.StartSpan(context.Background(), "run")
	_, p1 := c.StartSpan(ctx, "phase:start")
	p1.End(nil)
	_, p2 := c.StartSpan(ctx, "phase:llm_call")
	p2.End(nil)
	runSpan.End(nil)
	if err := c.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	items := cap.items()
	traces := byType(items, "trace-create")
	if len(traces) != 1 {
		t.Fatalf("got %d trace-create, want exactly 1 per run", len(traces))
	}
	traceID := traces[0].Body["id"]
	spans := byType(items, "span-create")
	if len(spans) != 3 {
		t.Fatalf("got %d span-create, want 3 (run + 2 phases)", len(spans))
	}
	for _, s := range spans {
		if s.Body["traceId"] != traceID {
			t.Fatalf("span %v has traceId %v, want %v", s.Body["name"], s.Body["traceId"], traceID)
		}
	}
}

func TestTraceIDsPrunedOnEnd(t *testing.T) {
	c, _ := newServerClient(t)
	ctx, run := c.StartSpan(context.Background(), "run")
	_, p1 := c.StartSpan(ctx, "phase:start")
	_, p2 := c.StartSpan(ctx, "phase:llm_call")

	c.mu.Lock()
	mid := len(c.traceIDs)
	c.mu.Unlock()
	if mid != 3 {
		t.Fatalf("traceIDs len = %d while 3 spans open, want 3", mid)
	}

	p1.End(nil)
	p2.End(nil)
	run.End(nil)

	c.mu.Lock()
	end := len(c.traceIDs)
	c.mu.Unlock()
	if end != 0 {
		t.Fatalf("traceIDs len = %d after all spans ended, want 0 (entries must be pruned)", end)
	}
}

func TestStartSpanPutsIDInContext(t *testing.T) {
	c, _ := newServerClient(t)
	ctx, _ := c.StartSpan(context.Background(), "outer")
	if spanIDFromContext(ctx) == "" {
		t.Fatal("StartSpan must store its span id in the returned context")
	}
}

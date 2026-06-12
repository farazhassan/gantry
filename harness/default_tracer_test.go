package harness_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry/harness"
)

func TestDefaultTracerRecordsSpanStartAndEnd(t *testing.T) {
	tr := harness.NewTrace()
	tracer := harness.NewDefaultTracer(tr)

	ctx, span := tracer.StartSpan(context.Background(), "phase:test")
	_ = ctx
	span.SetAttr("k", 1)
	span.RecordEvent("midpoint", map[string]any{"v": 2})
	span.End(nil)

	events := tr.Snapshot()
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
	if events[0].Kind != harness.KindSpanStart || events[0].Name != "phase:test" {
		t.Errorf("event[0] = %+v", events[0])
	}
	if events[1].Kind != harness.KindEvent || events[1].Name != "midpoint" {
		t.Errorf("event[1] = %+v", events[1])
	}
	if events[2].Kind != harness.KindSpanEnd || events[2].Err != nil {
		t.Errorf("event[2] = %+v", events[2])
	}
	if events[2].Duration <= 0 {
		t.Errorf("expected positive duration, got %v", events[2].Duration)
	}
	if events[2].Attrs["k"] != 1 {
		t.Errorf("expected attr k=1, got %+v", events[2].Attrs)
	}
}

func TestDefaultTracerSpanWithError(t *testing.T) {
	tr := harness.NewTrace()
	tracer := harness.NewDefaultTracer(tr)
	wantErr := errors.New("boom")

	_, span := tracer.StartSpan(context.Background(), "phase:err")
	span.End(wantErr)

	events := tr.Snapshot()
	end := events[len(events)-1]
	if !errors.Is(end.Err, wantErr) {
		t.Errorf("end.Err = %v, want %v", end.Err, wantErr)
	}
}

func TestDefaultTracerNestedSpans(t *testing.T) {
	tr := harness.NewTrace()
	tracer := harness.NewDefaultTracer(tr)

	ctx, outer := tracer.StartSpan(context.Background(), "outer")
	_, inner := tracer.StartSpan(ctx, "inner")
	inner.End(nil)
	outer.End(nil)

	events := tr.Snapshot()
	// events: outer-start, inner-start, inner-end, outer-end
	if len(events) != 4 {
		t.Fatalf("got %d events, want 4", len(events))
	}
	innerStart := events[1]
	outerStart := events[0]
	if innerStart.ParentID != outerStart.SpanID {
		t.Errorf("inner.ParentID = %q, want %q", innerStart.ParentID, outerStart.SpanID)
	}
}

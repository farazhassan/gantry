package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry/harness"
)

// TracerSuite verifies the contract of harness.Tracer.
func TracerSuite(t *testing.T, factory func() harness.Tracer) {
	t.Helper()

	t.Run("span_starts_and_ends", func(t *testing.T) {
		tr := factory()
		ctx, span := tr.StartSpan(context.Background(), "test")
		_ = ctx
		if span == nil {
			t.Fatalf("StartSpan returned nil Span")
		}
		span.SetAttr("k", "v")
		span.RecordEvent("evt", map[string]any{"x": 1})
		span.End(nil)
	})

	t.Run("span_can_record_error", func(t *testing.T) {
		tr := factory()
		_, span := tr.StartSpan(context.Background(), "err")
		span.End(errors.New("boom"))
	})

	t.Run("nested_spans_dont_panic", func(t *testing.T) {
		tr := factory()
		ctx, outer := tr.StartSpan(context.Background(), "outer")
		_, inner := tr.StartSpan(ctx, "inner")
		inner.End(nil)
		outer.End(nil)
	})
}

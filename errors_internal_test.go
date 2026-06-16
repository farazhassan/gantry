package gantry

import (
	"errors"
	"testing"
)

func TestTraceCarrierExtractsTrace(t *testing.T) {
	tr := NewTrace()
	tr.Record(TraceEvent{Name: "x", Kind: KindEvent})

	wrapped := wrapError(errors.New("boom"), tr)

	var tc TraceCarrier
	if !errors.As(wrapped, &tc) {
		t.Fatalf("wrapError result does not satisfy TraceCarrier")
	}
	if tc.Trace() != tr {
		t.Errorf("Trace mismatch")
	}
}

func TestWrapErrorUnwraps(t *testing.T) {
	inner := errors.New("inner")
	wrapped := wrapError(inner, NewTrace())
	if !errors.Is(wrapped, inner) {
		t.Errorf("errors.Is failed; wrapped should report inner")
	}
}

func TestWrapErrorNilReturnsNil(t *testing.T) {
	if got := wrapError(nil, NewTrace()); got != nil {
		t.Errorf("wrapError(nil) = %v, want nil", got)
	}
}

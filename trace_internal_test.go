package gantry

import (
	"context"
	"testing"
)

func TestTracerFrom_RoundTrips(t *testing.T) {
	tr := NewDefaultTracer(NewTrace())
	ctx := withTracer(context.Background(), tr)
	if got := tracerFrom(ctx); got != tr {
		t.Fatalf("tracerFrom = %v, want the tracer we put in", got)
	}
	if got := tracerFrom(context.Background()); got != nil {
		t.Fatalf("tracerFrom on a bare ctx = %v, want nil", got)
	}
}

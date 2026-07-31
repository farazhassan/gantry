package tool

import (
	"context"
	"testing"
)

func TestCallIDRoundTrip(t *testing.T) {
	ctx := WithCallID(context.Background(), "call-42")
	id, ok := CallIDFrom(ctx)
	if !ok || id != "call-42" {
		t.Errorf("CallIDFrom = (%q, %v), want (call-42, true)", id, ok)
	}
}

func TestCallIDFromAbsent(t *testing.T) {
	if _, ok := CallIDFrom(context.Background()); ok {
		t.Error("CallIDFrom on a bare context = ok true, want false")
	}
}

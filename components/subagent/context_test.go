package subagent

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
)

func TestDepthDefaultsToZero(t *testing.T) {
	if d := depthFrom(context.Background()); d != 0 {
		t.Errorf("depthFrom(Background) = %d, want 0", d)
	}
}

func TestDepthRoundTrip(t *testing.T) {
	ctx := withDepth(context.Background(), 2)
	if d := depthFrom(ctx); d != 2 {
		t.Errorf("depthFrom = %d, want 2", d)
	}
	ctx = withDepth(ctx, 3)
	if d := depthFrom(ctx); d != 3 {
		t.Errorf("nested depthFrom = %d, want 3 (inner value wins)", d)
	}
}

func TestUsageRecorderAccumulates(t *testing.T) {
	rec := &usageRecorder{}
	rec.add(gantry.Usage{InputTokens: 3, OutputTokens: 5, Cost: 0.25})
	rec.add(gantry.Usage{InputTokens: 7, OutputTokens: 11, Cost: 0.75})

	got := rec.total()
	want := gantry.Usage{InputTokens: 10, OutputTokens: 16, Cost: 1.0}
	if got != want {
		t.Errorf("total = %+v, want %+v", got, want)
	}
}

func TestUsageRecorderContextRoundTrip(t *testing.T) {
	rec := &usageRecorder{}
	ctx := withUsageRecorder(context.Background(), rec)

	got, ok := usageRecorderFrom(ctx)
	if !ok {
		t.Fatalf("usageRecorderFrom = (_, false), want the injected recorder")
	}
	if got != rec {
		t.Errorf("usageRecorderFrom returned a different recorder")
	}
}

func TestUsageRecorderAbsentFromBareContext(t *testing.T) {
	if _, ok := usageRecorderFrom(context.Background()); ok {
		t.Errorf("usageRecorderFrom(Background) = (_, true), want false")
	}
}

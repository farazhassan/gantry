package taskmanager

import (
	"context"
	"testing"
)

func TestSpawnCollectorAddDrainFIFO(t *testing.T) {
	c := &spawnCollector{}
	c.add("goal-1", "title-1")
	c.add("goal-2", "")

	got := c.drain()
	if len(got) != 2 {
		t.Fatalf("drain len = %d, want 2", len(got))
	}
	if got[0] != (spawnReq{goal: "goal-1", title: "title-1"}) {
		t.Errorf("got[0] = %+v, want {goal-1, title-1}", got[0])
	}
	if got[1] != (spawnReq{goal: "goal-2", title: ""}) {
		t.Errorf("got[1] = %+v, want {goal-2, \"\"}", got[1])
	}
	// Drain clears the buffer.
	if again := c.drain(); len(again) != 0 {
		t.Errorf("second drain len = %d, want 0 (buffer cleared)", len(again))
	}
}

func TestCollectorContextRoundTrip(t *testing.T) {
	c := &spawnCollector{}
	ctx := withCollector(context.Background(), c)

	got, ok := collectorFrom(ctx)
	if !ok {
		t.Fatalf("collectorFrom = (_, false), want the injected collector")
	}
	if got != c {
		t.Errorf("collectorFrom returned a different collector")
	}
}

func TestCollectorAbsentFromBareContext(t *testing.T) {
	if _, ok := collectorFrom(context.Background()); ok {
		t.Errorf("collectorFrom(Background) = (_, true), want false")
	}
}

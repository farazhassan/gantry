package taskmanager

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// newTestCollector builds a collector with deterministic minters and the
// default depth allowance, mirroring what TaskManager.newCollector wires in
// production. Shared by the collector and tool unit tests.
func newTestCollector() *spawnCollector {
	tn, sn := 0, 0
	return &spawnCollector{
		newTaskID:    func() string { tn++; return fmt.Sprintf("task-%d", tn) },
		newSessionID: func() string { sn++; return fmt.Sprintf("sess-%d", sn) },
		parentDepth:  0,
		maxDepth:     DefaultMaxSpawnDepth,
	}
}

func TestSpawnCollectorAddMintsAndDrainsFIFO(t *testing.T) {
	c := newTestCollector()
	id1, err := c.add("goal-1", "title-1", nil)
	if err != nil {
		t.Fatalf("add 1: %v", err)
	}
	id2, err := c.add("goal-2", "", nil)
	if err != nil {
		t.Fatalf("add 2: %v", err)
	}
	if id1 != "task-1" || id2 != "task-2" {
		t.Errorf("minted ids = (%q,%q), want (task-1, task-2)", id1, id2)
	}
	got := c.drain()
	if len(got) != 2 {
		t.Fatalf("drain len = %d, want 2", len(got))
	}
	if got[0].goal != "goal-1" || got[0].title != "title-1" || got[0].taskID != "task-1" ||
		got[0].sessionID != "" || got[0].dependsOn != nil {
		t.Errorf("got[0] = %+v, want {goal-1 title-1 task-1}", got[0])
	}
	if got[1].goal != "goal-2" || got[1].title != "" || got[1].taskID != "task-2" ||
		got[1].sessionID != "" || got[1].dependsOn != nil {
		t.Errorf("got[1] = %+v, want {goal-2 \"\" task-2}", got[1])
	}
	// Drain clears the buffer.
	if again := c.drain(); len(again) != 0 {
		t.Errorf("second drain len = %d, want 0 (buffer cleared)", len(again))
	}
}

func TestSpawnCollectorAddSessionMintsBothIDs(t *testing.T) {
	c := newTestCollector()
	sid, tid, err := c.addSession("new-1", "title-1", "")
	if err != nil {
		t.Fatalf("addSession: %v", err)
	}
	if sid != "sess-1" || tid != "task-1" {
		t.Errorf("ids = (%q,%q), want (sess-1, task-1)", sid, tid)
	}
	sess := c.drainSessions()
	if len(sess) != 1 {
		t.Fatalf("drainSessions len = %d, want 1", len(sess))
	}
	if sess[0].goal != "new-1" || sess[0].title != "title-1" || sess[0].taskID != "task-1" ||
		sess[0].sessionID != "sess-1" || sess[0].dependsOn != nil {
		t.Errorf("sessions = %+v, want one fully-populated req", sess)
	}
	if again := c.drainSessions(); len(again) != 0 {
		t.Errorf("second drainSessions = %+v, want empty", again)
	}
}

func TestSpawnCollectorBuffersIndependent(t *testing.T) {
	c := newTestCollector()
	if _, err := c.add("same-1", "", nil); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, _, err := c.addSession("new-1", "", ""); err != nil {
		t.Fatalf("addSession: %v", err)
	}
	if got := c.drainSessions(); len(got) != 1 || got[0].goal != "new-1" {
		t.Errorf("drainSessions = %+v, want [new-1]", got)
	}
	if got := c.drain(); len(got) != 1 || got[0].goal != "same-1" {
		t.Errorf("drain = %+v, want [same-1] (untouched by drainSessions)", got)
	}
}

func TestSpawnCollectorDepthGate(t *testing.T) {
	c := newTestCollector()
	c.parentDepth = DefaultMaxSpawnDepth // a child would be depth 4 > 3

	if _, err := c.add("g", "", nil); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Errorf("add at max depth: err = %v, want a depth error", err)
	}
	if _, _, err := c.addSession("g", "", ""); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Errorf("addSession at max depth: err = %v, want a depth error", err)
	}
	if got := c.drain(); len(got) != 0 {
		t.Errorf("rejected add buffered something: %+v", got)
	}
	if got := c.drainSessions(); len(got) != 0 {
		t.Errorf("rejected addSession buffered something: %+v", got)
	}
}

func TestCollectorContextRoundTrip(t *testing.T) {
	c := newTestCollector()
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

func TestCollectorCarriesRunIdentity(t *testing.T) {
	c := &spawnCollector{sessionID: "s1", taskID: "t1"}
	ctx := withCollector(context.Background(), c)

	got, ok := collectorFrom(ctx)
	if !ok {
		t.Fatalf("collectorFrom = (_, false), want the injected collector")
	}
	if got.sessionID != "s1" || got.taskID != "t1" {
		t.Errorf("identity = (%q, %q), want (s1, t1)", got.sessionID, got.taskID)
	}
}

func TestSpawnCollectorCarriesDependsOn(t *testing.T) {
	c := newTestCollector()
	id1, err := c.add("goal-1", "", nil)
	if err != nil {
		t.Fatalf("add 1: %v", err)
	}
	deps := []string{id1}
	id2, err := c.add("goal-2", "t2", deps)
	if err != nil {
		t.Fatalf("add 2: %v", err)
	}
	if id1 != "task-1" || id2 != "task-2" {
		t.Fatalf("minted ids = (%q, %q), want (task-1, task-2)", id1, id2)
	}
	// The collector must copy the deps slice, not alias the caller's.
	deps[0] = "mutated"

	got := c.drain()
	if len(got) != 2 {
		t.Fatalf("drain len = %d, want 2", len(got))
	}
	if got[0].dependsOn != nil {
		t.Errorf("got[0].dependsOn = %v, want nil", got[0].dependsOn)
	}
	if len(got[1].dependsOn) != 1 || got[1].dependsOn[0] != "task-1" {
		t.Errorf("got[1].dependsOn = %v, want [task-1] (isolated from caller mutation)", got[1].dependsOn)
	}
}

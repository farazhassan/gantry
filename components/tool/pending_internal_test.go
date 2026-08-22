package tool

import (
	"encoding/json"
	"testing"

	"github.com/farazhassan/gantry"
)

func TestSplitPendingID(t *testing.T) {
	cases := []struct {
		id         string
		wantOrigin string
		wantLeaf   string
		wantNested bool
	}{
		{"plain", "", "", false},
		{"origin1" + pendingIDSep + "leaf1", "origin1", "leaf1", true},
		{"o1" + pendingIDSep + "o2" + pendingIDSep + "leaf", "o1", "o2" + pendingIDSep + "leaf", true},
	}
	for _, c := range cases {
		gotOrigin, gotLeaf, gotNested := splitPendingID(c.id)
		if gotOrigin != c.wantOrigin || gotLeaf != c.wantLeaf || gotNested != c.wantNested {
			t.Errorf("splitPendingID(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.id, gotOrigin, gotLeaf, gotNested, c.wantOrigin, c.wantLeaf, c.wantNested)
		}
	}
}

func TestPendingEntriesRoundTripsThroughJSON(t *testing.T) {
	s := &gantry.State{}
	want := map[string]pendingEntry{
		"c1": {ToolName: "specialist", Resume: json.RawMessage(`{"step":1}`)},
	}
	setPendingEntries(s, want)

	// Simulate a checkpoint save/load: State.Meta is map[string]any, so
	// after a real JSON round-trip the stashed value decodes back as a
	// generic map[string]interface{}, not the original typed value.
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var reloaded gantry.State
	if err := json.Unmarshal(data, &reloaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	got := pendingEntriesFrom(&reloaded)
	if len(got) != 1 || got["c1"].ToolName != "specialist" || string(got["c1"].Resume) != `{"step":1}` {
		t.Errorf("pendingEntriesFrom after round-trip = %#v, want %#v", got, want)
	}
}

func TestSetPendingEntriesEmptyClearsMetaKey(t *testing.T) {
	s := &gantry.State{}
	setPendingEntries(s, map[string]pendingEntry{"c1": {ToolName: "x"}})
	setPendingEntries(s, map[string]pendingEntry{})
	if _, ok := s.Meta[pendingResumeMetaKey]; ok {
		t.Error("expected the Meta key to be removed when entries is empty")
	}
}

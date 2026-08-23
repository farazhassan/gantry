package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/checkpointer"
	checkpointermem "github.com/farazhassan/gantry/components/checkpointer/mem"
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

// TestPendingEntryPartialOmitemptyPreservesBackwardCompatibleShape guards
// pendingEntry.Partial's `omitempty` JSON tag: an entry with no partial
// answers yet (the common case — every flat, declared-client-tool pending
// call, and any nested entry before its group is ever answered
// incrementally) must marshal with no "partial" key at all, matching the
// JSON shape pendingEntry had before Partial was added. A stray `"partial":
// null` or `"partial": {}` key, while harmless to pendingEntriesFrom's own
// unmarshal, would still be a gratuitous shape change for anything else
// that happens to read state.Meta's raw JSON (e.g. a checkpoint diff/log).
func TestPendingEntryPartialOmitemptyPreservesBackwardCompatibleShape(t *testing.T) {
	entry := pendingEntry{ToolName: "specialist", Resume: json.RawMessage(`{"step":1}`)}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(data, []byte(`"partial"`)) {
		t.Errorf("marshaled pendingEntry with nil Partial = %s, want no \"partial\" key at all", data)
	}
}

// TestPendingEntryResumeSurvivesCheckpointRoundTripByteForByte guards
// against the failure mode described on pendingEntry.MarshalJSON: a
// Resume payload containing a large integer (beyond float64's 2^53 exact
// range) must come back byte-identical after a REAL checkpoint save/load,
// not just after pendingEntriesFrom's own re-marshal/re-unmarshal. Before
// Resume was carried as base64, checkpointer.StoreCheckpointer's
// json.Marshal(state)/json.Unmarshal(data, &state) round-trip decoded
// state.Meta generically, turning any JSON number embedded in Resume into
// a float64 and silently rounding it (e.g. 9007199254740993, which is
// 2^53+1, became 9007199254740992) by the time pendingEntriesFrom ever ran
// — a loss that had already happened upstream, outside pendingEntriesFrom's
// control, and that its own idempotent-looking re-marshal could not
// recover from.
func TestPendingEntryResumeSurvivesCheckpointRoundTripByteForByte(t *testing.T) {
	const wantResume = `{"id":9007199254740993}` // 2^53 + 1: not exactly representable as float64

	s := gantry.NewState("hi")
	setPendingEntries(s, map[string]pendingEntry{
		"c1": {ToolName: "specialist", Resume: json.RawMessage(wantResume)},
	})

	cp, err := checkpointer.FromStore(checkpointermem.NewStore())
	if err != nil {
		t.Fatalf("FromStore: %v", err)
	}
	ctx := context.Background()
	if err := cp.Save(ctx, "run1", s); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := cp.Load(ctx, "run1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := pendingEntriesFrom(loaded)
	if len(got) != 1 {
		t.Fatalf("pendingEntriesFrom after checkpoint round-trip = %#v, want 1 entry", got)
	}
	if gotResume := string(got["c1"].Resume); gotResume != wantResume {
		t.Errorf("Resume after checkpoint round-trip = %s, want %s (byte-for-byte)", gotResume, wantResume)
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

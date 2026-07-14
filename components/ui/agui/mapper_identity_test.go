package agui

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/farazhassan/gantry"
)

func TestMapperForwardsIdentityOnceAfterRunStarted(t *testing.T) {
	m := NewMapper("t1", "r1")
	got := m.Map(gantry.Event{
		Type:      gantry.EventPhaseStart,
		Phase:     gantry.PhaseStart,
		RunID:     "run-aabbccdd00112233",
		SessionID: "sess-1",
		TaskID:    "task-1",
		Agent:     "writer",
	})
	want := []Event{
		newRunStarted("t1", "r1"),
		newCustom(identityName, runIdentity{
			RunID:     "run-aabbccdd00112233",
			SessionID: "sess-1",
			TaskID:    "task-1",
			Agent:     "writer",
		}),
		newStepStarted("start"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
	// Identity is forwarded only once per run.
	got2 := m.Map(gantry.Event{Type: gantry.EventPhaseEnd, Phase: gantry.PhaseStart, RunID: "run-aabbccdd00112233"})
	want2 := []Event{newStepFinished("start")}
	if !reflect.DeepEqual(got2, want2) {
		t.Fatalf("got  %#v\nwant %#v", got2, want2)
	}
}

func TestMapperNoIdentityNoCustomFrame(t *testing.T) {
	m := NewMapper("t1", "r1")
	got := m.Map(gantry.Event{Type: gantry.EventPhaseStart, Phase: gantry.PhaseStart})
	want := []Event{
		newRunStarted("t1", "r1"),
		newStepStarted("start"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v (no CUSTOM frame for identity-less events)", got, want)
	}
}

func TestMapperIdentityOnLaterEventStillForwarded(t *testing.T) {
	m := NewMapper("t1", "r1")
	_ = m.Map(gantry.Event{Type: gantry.EventPhaseStart, Phase: gantry.PhaseStart}) // no identity yet
	got := m.Map(gantry.Event{Type: gantry.EventPhaseEnd, Phase: gantry.PhaseStart, RunID: "run-1122334455667788"})
	want := []Event{
		newCustom(identityName, runIdentity{RunID: "run-1122334455667788"}),
		newStepFinished("start"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestCustomIdentityWireShape(t *testing.T) {
	b, err := json.Marshal(newCustom(identityName, runIdentity{RunID: "run-aabbccdd00112233", Agent: "writer"}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"type":"CUSTOM","name":"gantry.identity","value":{"runId":"run-aabbccdd00112233","agent":"writer"}}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
}

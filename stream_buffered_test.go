package gantry

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEventDroppedJSONShape(t *testing.T) {
	// Zero Dropped must be omitted so existing consumers see no new key.
	b, err := json.Marshal(Event{Type: EventTextDelta, TextDelta: "x"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "dropped") {
		t.Errorf("zero Dropped must be omitted from JSON: %s", b)
	}
	// Non-zero Dropped serializes under the documented key.
	b, err = json.Marshal(Event{Type: EventTextDelta, Dropped: 3})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"dropped":3`) {
		t.Errorf("Dropped not serialized as \"dropped\": %s", b)
	}
}

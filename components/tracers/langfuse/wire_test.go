package langfuse

import (
	"errors"
	"testing"
	"time"
)

func TestTraceCreateItem(t *testing.T) {
	start := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	it := traceCreateItem("trace-1", "phase:plan", start)
	if it.Type != "trace-create" {
		t.Fatalf("Type = %q, want trace-create", it.Type)
	}
	if it.ID == "" {
		t.Fatal("envelope ID must be non-empty")
	}
	if got := it.Body["id"]; got != "trace-1" {
		t.Fatalf("body id = %v, want trace-1", got)
	}
	if got := it.Body["name"]; got != "phase:plan" {
		t.Fatalf("body name = %v, want phase:plan", got)
	}
}

func TestSpanCreateItem(t *testing.T) {
	start := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Second)

	root := spanCreateItem("t1", "s1", "", "root", start, end, map[string]any{"iteration": 1}, nil)
	if root.Type != "span-create" {
		t.Fatalf("Type = %q, want span-create", root.Type)
	}
	if root.Body["id"] != "s1" || root.Body["traceId"] != "t1" {
		t.Fatalf("ids = %v/%v, want s1/t1", root.Body["id"], root.Body["traceId"])
	}
	if _, ok := root.Body["parentObservationId"]; ok {
		t.Fatal("root span must not set parentObservationId")
	}
	md, ok := root.Body["metadata"].(map[string]any)
	if !ok || md["iteration"] != 1 {
		t.Fatalf("metadata = %v, want iteration=1", root.Body["metadata"])
	}

	child := spanCreateItem("t1", "s2", "s1", "child", start, end, nil, errors.New("boom"))
	if child.Body["parentObservationId"] != "s1" {
		t.Fatalf("parentObservationId = %v, want s1", child.Body["parentObservationId"])
	}
	if child.Body["level"] != "ERROR" || child.Body["statusMessage"] != "boom" {
		t.Fatalf("error mapping = %v/%v, want ERROR/boom", child.Body["level"], child.Body["statusMessage"])
	}
	if _, ok := child.Body["metadata"]; ok {
		t.Fatal("nil attrs must omit metadata")
	}
}

func TestEventCreateItem(t *testing.T) {
	start := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	it := eventCreateItem("t1", "s1", "tool_call", start, map[string]any{"tool": "search"})
	if it.Type != "event-create" {
		t.Fatalf("Type = %q, want event-create", it.Type)
	}
	if it.Body["traceId"] != "t1" || it.Body["parentObservationId"] != "s1" {
		t.Fatalf("ids = %v/%v, want t1/s1", it.Body["traceId"], it.Body["parentObservationId"])
	}
	if it.Body["name"] != "tool_call" {
		t.Fatalf("name = %v, want tool_call", it.Body["name"])
	}

	if id, _ := it.Body["id"].(string); id == "" {
		t.Fatal("event body must carry a non-empty observation id")
	}

	noParent := eventCreateItem("t1", "", "evt", start, nil)
	if _, ok := noParent.Body["parentObservationId"]; ok {
		t.Fatal("event with empty parentID must omit parentObservationId")
	}
	if _, ok := noParent.Body["metadata"]; ok {
		t.Fatal("event with nil attrs must omit metadata")
	}
}

func TestNewIDUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := newID()
		if id == "" || seen[id] {
			t.Fatalf("duplicate or empty id: %q", id)
		}
		seen[id] = true
	}
}

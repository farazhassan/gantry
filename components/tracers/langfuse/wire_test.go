package langfuse

import (
	"errors"
	"testing"
	"time"

	"github.com/farazhassan/gantry"
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

func TestGenerationCreateItem(t *testing.T) {
	start := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Second)
	attrs := map[string]any{
		gantry.SpanKindKey:  gantry.SpanKindGeneration,
		gantry.AttrModel:    "claude-x",
		gantry.AttrInput:    `[{"role":"user","content":"hi"}]`,
		gantry.AttrOutput:   `{"role":"assistant","content":"yo"}`,
		gantry.AttrUsageIn:  10,
		gantry.AttrUsageOut: 3,
		"custom":            "keep",
	}
	it := generationCreateItem("t1", "s1", "run1", "model.call", start, end, attrs, nil)

	if it.Type != "generation-create" {
		t.Fatalf("Type = %q, want generation-create", it.Type)
	}
	if it.Body["model"] != "claude-x" {
		t.Errorf("model = %v, want claude-x", it.Body["model"])
	}
	if it.Body["parentObservationId"] != "run1" {
		t.Errorf("parentObservationId = %v, want run1", it.Body["parentObservationId"])
	}
	if _, ok := it.Body["input"].([]any); !ok {
		t.Errorf("input not decoded to JSON array: %T", it.Body["input"])
	}
	if _, ok := it.Body["output"].(map[string]any); !ok {
		t.Errorf("output not decoded to JSON object: %T", it.Body["output"])
	}
	usage, _ := it.Body["usage"].(map[string]any)
	if usage["input"] != 10 || usage["output"] != 3 || usage["unit"] != "TOKENS" {
		t.Errorf("usage = %v, want input=10 output=3 unit=TOKENS", usage)
	}
	md, _ := it.Body["metadata"].(map[string]any)
	if md["custom"] != "keep" {
		t.Errorf("metadata dropped custom key: %v", md)
	}
	if _, ok := md[gantry.SpanKindKey]; ok {
		t.Error("kind marker must be stripped from metadata")
	}
	if _, ok := md[gantry.AttrModel]; ok {
		t.Error("model must be promoted out of metadata")
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

func TestGenerationCreateItemError(t *testing.T) {
	start := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Second)
	// An errored generation records only its input at start; output, usage, and
	// model are skipped (see generation.end), so the item must carry the error
	// mapping and none of the success-only native fields.
	attrs := map[string]any{
		gantry.SpanKindKey: gantry.SpanKindGeneration,
		gantry.AttrInput:   `[{"role":"user","content":"hi"}]`,
	}
	it := generationCreateItem("t1", "s1", "run1", "model.call", start, end, attrs, errors.New("boom"))
	if it.Type != "generation-create" {
		t.Fatalf("Type = %q, want generation-create", it.Type)
	}
	if it.Body["level"] != "ERROR" || it.Body["statusMessage"] != "boom" {
		t.Errorf("error mapping = %v/%v, want ERROR/boom", it.Body["level"], it.Body["statusMessage"])
	}
	if _, ok := it.Body["usage"]; ok {
		t.Error("errored generation must not emit a usage object")
	}
	if _, ok := it.Body["output"]; ok {
		t.Error("errored generation must not emit output")
	}
}

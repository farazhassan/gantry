package harness_test

import (
	"testing"

	"github.com/farazhassan/gantry/harness"
)

func TestDocumentFields(t *testing.T) {
	d := harness.Document{
		ID:       "doc-1",
		Content:  "hello",
		Score:    0.85,
		Metadata: map[string]any{"source": "wiki"},
	}
	if d.ID != "doc-1" || d.Content != "hello" {
		t.Errorf("unexpected document fields: %+v", d)
	}
	if d.Metadata["source"] != "wiki" {
		t.Errorf("metadata mismatch: %+v", d.Metadata)
	}
}

func TestPlanZeroValue(t *testing.T) {
	var p harness.Plan
	if p.Goal != "" || len(p.Steps) != 0 {
		t.Errorf("zero Plan non-empty: %+v", p)
	}
}

func TestUsageAdd(t *testing.T) {
	a := harness.Usage{InputTokens: 100, OutputTokens: 50, Cost: 0.01}
	b := harness.Usage{InputTokens: 25, OutputTokens: 10, Cost: 0.005}
	got := a.Add(b)
	want := harness.Usage{InputTokens: 125, OutputTokens: 60, Cost: 0.015}
	if got != want {
		t.Errorf("Usage.Add = %+v, want %+v", got, want)
	}
}

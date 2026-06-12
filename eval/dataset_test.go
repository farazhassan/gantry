package eval_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/farazhassan/gantry/eval"
)

func TestJSONLDatasetLoads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cases.jsonl")
	body := `{"id":"c1","input":"hello","metadata":{"expected":"hi"}}
{"id":"c2","input":"bye","metadata":{"expected":"farewell"}}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ds := eval.JSONLDataset(path)
	cases, err := ds.Cases(context.Background())
	if err != nil {
		t.Fatalf("Cases: %v", err)
	}
	if len(cases) != 2 {
		t.Fatalf("len = %d, want 2", len(cases))
	}
	if cases[0].ID != "c1" || cases[0].Input != "hello" {
		t.Errorf("c1 = %+v", cases[0])
	}
	if cases[0].Metadata["expected"] != "hi" {
		t.Errorf("metadata.expected = %v", cases[0].Metadata["expected"])
	}
}

func TestJSONLDatasetSkipsBlankLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cases.jsonl")
	body := "\n{\"id\":\"a\",\"input\":\"x\"}\n\n"
	os.WriteFile(path, []byte(body), 0o644)
	cases, err := eval.JSONLDataset(path).Cases(context.Background())
	if err != nil {
		t.Fatalf("Cases: %v", err)
	}
	if len(cases) != 1 {
		t.Errorf("len = %d, want 1", len(cases))
	}
}

func TestSliceDatasetReturnsItems(t *testing.T) {
	ds := eval.SliceDataset([]eval.Case{
		{ID: "a", Input: "x"},
		{ID: "b", Input: "y"},
	})
	cases, _ := ds.Cases(context.Background())
	if len(cases) != 2 {
		t.Errorf("len = %d, want 2", len(cases))
	}
}

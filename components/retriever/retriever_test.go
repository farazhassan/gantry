package retriever_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/retriever"
)

func TestStaticRetrieverReturnsConfiguredDocs(t *testing.T) {
	docs := []gantry.Document{
		{ID: "a", Content: "alpha", Score: 0.9},
		{ID: "b", Content: "beta", Score: 0.7},
	}
	r := retriever.NewStatic(docs)
	got, err := r.Retrieve(context.Background(), "anything", 10)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
}

func TestStaticRetrieverHonorsK(t *testing.T) {
	docs := []gantry.Document{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	r := retriever.NewStatic(docs)
	got, _ := r.Retrieve(context.Background(), "q", 2)
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
}

func TestInterface(t *testing.T) {
	var _ retriever.Retriever = retriever.NewStatic(nil)
}

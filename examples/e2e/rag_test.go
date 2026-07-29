package main

import (
	"context"
	"strings"
	"testing"
)

// TestKnowledgeBaseRecallsArithmeticFact verifies the vector-RAG demo actually
// retrieves the relevant fact: the seeded memory store, queried with the
// example's own question, ranks the arithmetic fact ahead of the distractors.
// This pins the pedagogy — if the stand-in embedder or the seed facts drift so
// that recall stops surfacing the right fact, this fails.
func TestKnowledgeBaseRecallsArithmeticFact(t *testing.T) {
	emb := docEmbedder{}
	kb, err := newKnowledgeBase(emb)
	if err != nil {
		t.Fatalf("newKnowledgeBase: %v", err)
	}
	qv, err := emb.Embed(context.Background(), []string{"what is 2 + 3?"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	hits, err := kb.Search(context.Background(), qv[0], 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1", len(hits))
	}
	if !strings.Contains(hits[0].Text, "calc tool") {
		t.Errorf("top hit = %q, want the arithmetic fact", hits[0].Text)
	}
}

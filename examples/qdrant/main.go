package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/farazhassan/gantry"
	embopenai "github.com/farazhassan/gantry/components/embeddings/openai"
	"github.com/farazhassan/gantry/components/llm/openai"
	"github.com/farazhassan/gantry/components/qdrant"
	"github.com/farazhassan/gantry/components/retriever"
)

const (
	collection = "gantry-docs"
	embedDim   = 1536 // text-embedding-3-small
	embedModel = "text-embedding-3-small"
	chatModel  = "gpt-4o-mini"
	topK       = 3
)

var seedDocs = []struct {
	ID   uint64
	Text string
}{
	{1, "Gantry is a phase-based agent framework for Go."},
	{2, "Gantry components attach to the agent as middleware."},
	{3, "Gantry talks to LLM providers over plain HTTP, with no vendor SDKs."},
}

func main() {
	ingest := flag.Bool("ingest", false, "embed and upsert seed documents, then exit")
	flag.Parse()

	emb := embopenai.New(embedModel) // reads OPENAI_API_KEY
	store := qdrant.New(
		qdrant.WithCollection(collection),
		qdrant.WithDim(embedDim),
		qdrant.WithBaseURL(getenv("QDRANT_URL", "http://localhost:6333")),
		qdrant.WithAPIKey(os.Getenv("QDRANT_API_KEY")),
	)
	ctx := context.Background()

	if *ingest {
		if err := runIngest(ctx, store, emb); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("ingested %d documents into %q\n", len(seedDocs), collection)
		return
	}

	if err := runQuery(ctx, store, emb); err != nil {
		log.Fatal(err)
	}
}

func runIngest(ctx context.Context, store *qdrant.Store, emb *embopenai.Client) error {
	if err := store.EnsureCollection(ctx); err != nil {
		return err
	}
	texts := make([]string, len(seedDocs))
	for i, d := range seedDocs {
		texts[i] = d.Text
	}
	vecs, err := emb.Embed(ctx, texts)
	if err != nil {
		return err
	}
	points := make([]qdrant.Point, len(seedDocs))
	for i, d := range seedDocs {
		points[i] = qdrant.Point{
			ID:      d.ID,
			Vector:  vecs[i],
			Payload: map[string]any{"content": d.Text},
		}
	}
	return store.Upsert(ctx, points...)
}

func runQuery(ctx context.Context, store *qdrant.Store, emb *embopenai.Client) error {
	llm := openai.New(chatModel) // reads OPENAI_API_KEY
	a, err := gantry.NewAgent(gantry.WithLLM(llm))
	if err != nil {
		return err
	}
	retriever.WithRetriever(a, qdrant.NewRetriever(store, emb), topK)

	state, err := a.Run(ctx, "How does Gantry talk to LLM providers?")
	if err != nil {
		return err
	}
	fmt.Printf("retrieved %d docs\n", len(state.Retrieved))
	fmt.Println("answer:", state.FinalOutput)
	return nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

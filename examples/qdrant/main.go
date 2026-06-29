package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/farazhassan/gantry"
	embopenai "github.com/farazhassan/gantry/components/embeddings/openai"
	"github.com/farazhassan/gantry/components/llm/openai"
	"github.com/farazhassan/gantry/components/qdrant"
	"github.com/farazhassan/gantry/components/retriever"
)

const (
	collection = "gantry-docs"
	topK       = 3
)

// Provider settings. The defaults target OpenAI; override the env vars to point
// at any OpenAI-compatible server (e.g. Ollama at http://localhost:11434).
var (
	// LLM_BASE_URL: API root, without the /v1 suffix. Empty uses the OpenAI
	// default. For Ollama: http://localhost:11434
	baseURL = os.Getenv("LLM_BASE_URL")
	// EMBED_MODEL / CHAT_MODEL: model names for the two roles.
	embedModel = getenv("EMBED_MODEL", "text-embedding-3-small")
	chatModel  = getenv("CHAT_MODEL", "gpt-4o-mini")
	// EMBED_DIM: vector size of the embedding model — must match the model.
	// text-embedding-3-small is 1536; Ollama's nomic-embed-text is 768.
	embedDim = atoi(getenv("EMBED_DIM", "1536"))
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

	emb := embopenai.New(embedModel, embedOpts()...)
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
	llm := openai.New(chatModel, chatOpts()...)
	a, err := gantry.NewAgent(gantry.WithLLM(llm))
	if err != nil {
		return err
	}
	if err := a.With(retriever.New(qdrant.NewRetriever(store, emb), topK)); err != nil {
		return err
	}

	state, err := a.Run(ctx, "How does Gantry talk to LLM providers?")
	if err != nil {
		return err
	}
	fmt.Printf("retrieved %d docs\n", len(state.Retrieved))
	fmt.Println("answer:", state.FinalOutput)
	return nil
}

// embedOpts and chatOpts apply the shared base URL and API key. They differ
// only in the Option type each adapter defines.
func embedOpts() []embopenai.Option {
	var opts []embopenai.Option
	if baseURL != "" {
		opts = append(opts, embopenai.WithBaseURL(baseURL))
	}
	opts = append(opts, embopenai.WithAPIKey(apiKey()))
	return opts
}

func chatOpts() []openai.Option {
	var opts []openai.Option
	if baseURL != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}
	opts = append(opts, openai.WithAPIKey(apiKey()))
	return opts
}

// apiKey returns OPENAI_API_KEY, or a placeholder when pointed at a custom
// base URL (e.g. Ollama) that ignores auth — the adapters require a non-empty
// key. Against the default OpenAI endpoint an empty key is left as-is so the
// adapter panics with a clear "missing key" message.
func apiKey() string {
	if k := os.Getenv("OPENAI_API_KEY"); k != "" {
		return k
	}
	if baseURL != "" {
		return "ollama" // any non-empty value; keyless servers ignore it
	}
	return ""
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		log.Fatalf("EMBED_DIM must be an integer, got %q: %v", s, err)
	}
	return n
}

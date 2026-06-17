package embeddings

import "context"

// Embeddings turns text into dense vectors. The API is batch-oriented because
// every embedding provider is: one round trip can embed many texts.
type Embeddings interface {
	// Embed returns one vector per input text, in input order: the result has
	// the same length as texts. An empty texts slice returns an empty result
	// and no error (no network call).
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

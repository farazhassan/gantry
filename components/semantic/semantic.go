package semantic

import "context"

// Item is one memory: the text, its embedding, and optional metadata.
type Item struct {
	Text     string
	Vector   []float32
	Metadata map[string]any
}

// Hit is a recalled Item with a similarity score in [-1, 1] (higher = more
// similar). Score is normalized similarity (1 - cosine distance), matching
// the higher-is-better semantics of gantry.Document.Score.
//
// Implementations may leave Hit.Vector nil in search results; callers must
// not rely on it being populated.
type Hit struct {
	Item
	Score float64
}

// Store persists embedded memories and recalls the nearest by vector
// similarity. Search returns at most k hits ordered by descending Score;
// k <= 0 returns an empty result and no error. Hits are independent at the
// slice/struct level, but Metadata maps may alias the store — treat returned
// hits as read-only. Add retains the caller's Vector slices and Metadata
// maps; callers must not mutate an Item's reference-typed fields after Add.
type Store interface {
	Add(ctx context.Context, items ...Item) error
	Search(ctx context.Context, vector []float32, k int) ([]Hit, error)
}

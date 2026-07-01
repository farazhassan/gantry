package checkpointer

import "context"

// Store persists opaque blobs by id. Implementations own the backend (file, SQL,
// in-memory, …); StoreCheckpointer owns State serialization and field projection.
type Store interface {
	// Put inserts or replaces the blob stored under id.
	Put(ctx context.Context, id string, blob []byte) error
	// Get returns the blob stored under id. found is false (with a nil error)
	// when no blob exists; StoreCheckpointer maps that to a wrapped ErrNotFound.
	Get(ctx context.Context, id string) (blob []byte, found bool, err error)
}

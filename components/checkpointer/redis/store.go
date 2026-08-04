package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/farazhassan/gantry/components/checkpointer"
)

// Store persists blobs as Redis string values under a prefixed key. It accepts
// any goredis.Cmdable (*goredis.Client, *goredis.ClusterClient, *goredis.Ring, …),
// so callers own connection setup, TLS, auth, and pooling.
type Store struct {
	rdb    goredis.Cmdable
	prefix string
	ttl    time.Duration // 0 ⇒ no expiration
}

// Option configures a Store.
type Option func(*Store)

// WithKeyPrefix sets the prefix prepended to every id (default "gantry:checkpoint:").
func WithKeyPrefix(prefix string) Option { return func(s *Store) { s.prefix = prefix } }

// WithTTL sets an expiration applied on every Put (default 0, meaning no
// expiration). Reads of an expired key behave like a missing id: Get returns
// found == false.
func WithTTL(d time.Duration) Option { return func(s *Store) { s.ttl = d } }

// New returns a Store backed by rdb. rdb must be non-nil.
func New(rdb goredis.Cmdable, opts ...Option) *Store {
	s := &Store{rdb: rdb, prefix: "gantry:checkpoint:"}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Store) key(id string) string { return s.prefix + id }

func (s *Store) Put(ctx context.Context, id string, blob []byte) error {
	if err := s.rdb.Set(ctx, s.key(id), blob, s.ttl).Err(); err != nil {
		return fmt.Errorf("checkpointer/redis: SET %q: %w", id, err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, id string) ([]byte, bool, error) {
	blob, err := s.rdb.Get(ctx, s.key(id)).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("checkpointer/redis: GET %q: %w", id, err)
	}
	return blob, true, nil
}

var _ checkpointer.Store = (*Store)(nil)

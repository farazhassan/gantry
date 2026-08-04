package redis_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/components/checkpointer/redis"
	"github.com/farazhassan/gantry/conformance"
)

// compile-time guarantee the backend satisfies checkpointer.Store.
var _ checkpointer.Store = (*redis.Store)(nil)

func newClient(t *testing.T) *goredis.Client {
	t.Helper()
	mr := miniredis.RunT(t) // t.Cleanup(mr.Close) is registered by RunT
	return goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
}

func TestConformance(t *testing.T) {
	rdb := newClient(t)
	conformance.CheckpointerSuite(t, func() checkpointer.Checkpointer {
		c, err := checkpointer.FromStore(redis.New(rdb))
		if err != nil {
			t.Fatalf("FromStore: %v", err)
		}
		return c
	})
}

func TestKeyPrefixIsApplied(t *testing.T) {
	rdb := newClient(t)
	s := redis.New(rdb, redis.WithKeyPrefix("myapp:ckpt:"))
	ctx := context.Background()

	if err := s.Put(ctx, "id-1", []byte("blob")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := rdb.Get(ctx, "myapp:ckpt:id-1").Bytes()
	if err != nil {
		t.Fatalf("expected key stored under prefix, Get: %v", err)
	}
	if string(got) != "blob" {
		t.Errorf("got = %q, want %q", got, "blob")
	}
}

func TestGetMissingReturnsNotFound(t *testing.T) {
	rdb := newClient(t)
	s := redis.New(rdb)
	_, found, err := s.Get(context.Background(), "ghost")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if found {
		t.Errorf("found = true, want false for missing id")
	}
}

func TestTTLExpiresEntry(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	s := redis.New(rdb, redis.WithTTL(time.Minute))
	ctx := context.Background()

	if err := s.Put(ctx, "id-1", []byte("blob")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, found, err := s.Get(ctx, "id-1"); err != nil || !found {
		t.Fatalf("Get before expiry: found=%v err=%v", found, err)
	}

	mr.FastForward(2 * time.Minute)

	_, found, err := s.Get(ctx, "id-1")
	if err != nil {
		t.Fatalf("Get after expiry: %v", err)
	}
	if found {
		t.Errorf("found = true after TTL expiry, want false")
	}
}

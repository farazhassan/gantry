package checkpointer_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/components/checkpointer"
)

func TestMemStore_PutGetRoundTrip(t *testing.T) {
	s := checkpointer.NewMemStore()
	if err := s.Put(context.Background(), "k", []byte("hello")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, found, err := s.Get(context.Background(), "k")
	if err != nil || !found {
		t.Fatalf("Get: found=%v err=%v", found, err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q, want hello", got)
	}
}

func TestMemStore_GetMissing(t *testing.T) {
	_, found, err := checkpointer.NewMemStore().Get(context.Background(), "nope")
	if err != nil || found {
		t.Fatalf("missing key: found=%v err=%v", found, err)
	}
}

func TestMemStore_CopiesOnPutAndGet(t *testing.T) {
	s := checkpointer.NewMemStore()
	in := []byte("abc")
	_ = s.Put(context.Background(), "k", in)
	in[0] = 'X' // mutate caller's slice after Put
	got, _, _ := s.Get(context.Background(), "k")
	if string(got) != "abc" {
		t.Fatalf("Put aliased caller slice: %q", got)
	}
	got[0] = 'Y' // mutate returned slice
	again, _, _ := s.Get(context.Background(), "k")
	if string(again) != "abc" {
		t.Fatalf("Get aliased stored slice: %q", again)
	}
}

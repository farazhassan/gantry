package offload

import (
	"context"
	"testing"
)

func TestMemStorePutIsIdempotent(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	if err := s.Put(ctx, "k", "first"); err != nil {
		t.Fatal(err)
	}
	_ = s.Put(ctx, "k", "second")
	v, ok, err := s.Get(ctx, "k")
	if err != nil || !ok || v != "first" {
		t.Errorf("Get = %q, %v, %v; want first, true, nil", v, ok, err)
	}
	if _, ok, _ := s.Get(ctx, "missing"); ok {
		t.Error("Get(missing) reported found")
	}
}

func TestRefIsContentDerived(t *testing.T) {
	a, b := refFor("x"), refFor("x")
	if a != b || len(a) != len("res_")+16 || a[:4] != "res_" {
		t.Errorf("refFor = %q / %q", a, b)
	}
	if refFor("y") == a {
		t.Error("different content, same ref")
	}
}

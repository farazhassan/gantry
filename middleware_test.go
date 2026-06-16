package gantry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
)

func TestComposeRegistrationOrderInnermostFirst(t *testing.T) {
	// Registration order: A then B then C.
	// Expected wrap: C( B( A( inner ) ) ) — C is outermost.
	// Execution: C-pre, B-pre, A-pre, inner, A-post, B-post, C-post.
	var log []string

	mk := func(name string) gantry.Middleware {
		return func(next gantry.Handler) gantry.Handler {
			return func(ctx context.Context, s *gantry.State) error {
				log = append(log, name+"-pre")
				err := next(ctx, s)
				log = append(log, name+"-post")
				return err
			}
		}
	}

	inner := func(ctx context.Context, s *gantry.State) error {
		log = append(log, "inner")
		return nil
	}

	chain := gantry.Compose(inner, []gantry.Middleware{mk("A"), mk("B"), mk("C")})
	if err := chain(context.Background(), gantry.NewState("")); err != nil {
		t.Fatalf("chain error: %v", err)
	}

	want := []string{"C-pre", "B-pre", "A-pre", "inner", "A-post", "B-post", "C-post"}
	if len(log) != len(want) {
		t.Fatalf("got %v, want %v", log, want)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Errorf("log[%d] = %q, want %q", i, log[i], want[i])
		}
	}
}

func TestComposeShortCircuit(t *testing.T) {
	wantErr := errors.New("short")
	calls := 0
	outer := func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			return wantErr
		}
	}
	inner := func(ctx context.Context, s *gantry.State) error {
		calls++
		return nil
	}

	chain := gantry.Compose(inner, []gantry.Middleware{outer})
	err := chain(context.Background(), gantry.NewState(""))
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
	if calls != 0 {
		t.Errorf("inner called %d times, want 0", calls)
	}
}

func TestComposeNilInner(t *testing.T) {
	chain := gantry.Compose(nil, nil)
	if err := chain(context.Background(), gantry.NewState("")); err != nil {
		t.Errorf("nil inner should be no-op; got err %v", err)
	}
}

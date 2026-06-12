package harness_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry/harness"
)

func TestComposeRegistrationOrderInnermostFirst(t *testing.T) {
	// Registration order: A then B then C.
	// Expected wrap: C( B( A( inner ) ) ) — C is outermost.
	// Execution: C-pre, B-pre, A-pre, inner, A-post, B-post, C-post.
	var log []string

	mk := func(name string) harness.Middleware {
		return func(next harness.Handler) harness.Handler {
			return func(ctx context.Context, s *harness.State) error {
				log = append(log, name+"-pre")
				err := next(ctx, s)
				log = append(log, name+"-post")
				return err
			}
		}
	}

	inner := func(ctx context.Context, s *harness.State) error {
		log = append(log, "inner")
		return nil
	}

	chain := harness.Compose(inner, []harness.Middleware{mk("A"), mk("B"), mk("C")})
	if err := chain(context.Background(), harness.NewState("")); err != nil {
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
	outer := func(next harness.Handler) harness.Handler {
		return func(ctx context.Context, s *harness.State) error {
			return wantErr
		}
	}
	inner := func(ctx context.Context, s *harness.State) error {
		calls++
		return nil
	}

	chain := harness.Compose(inner, []harness.Middleware{outer})
	err := chain(context.Background(), harness.NewState(""))
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
	if calls != 0 {
		t.Errorf("inner called %d times, want 0", calls)
	}
}

func TestComposeNilInner(t *testing.T) {
	chain := harness.Compose(nil, nil)
	if err := chain(context.Background(), harness.NewState("")); err != nil {
		t.Errorf("nil inner should be no-op; got err %v", err)
	}
}

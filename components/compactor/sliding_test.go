package compactor_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/compactor"
)

func TestSlidingWindowKeepsLastN(t *testing.T) {
	c := compactor.NewSlidingWindow(2)
	msgs := []gantry.Message{
		{Content: "a"}, {Content: "b"}, {Content: "c"}, {Content: "d"},
	}
	got, err := c.Compact(context.Background(), msgs, compactor.Budget{})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Content != "c" || got[1].Content != "d" {
		t.Errorf("got = %+v", got)
	}
}

func TestSlidingWindowAllowsLargerInput(t *testing.T) {
	c := compactor.NewSlidingWindow(10)
	msgs := []gantry.Message{{Content: "a"}, {Content: "b"}}
	got, _ := c.Compact(context.Background(), msgs, compactor.Budget{})
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
}

func TestCompactorInterface(t *testing.T) {
	var _ compactor.Compactor = compactor.NewSlidingWindow(5)
}

func TestNewSlidingWindowValidatesN(t *testing.T) {
	tests := []struct {
		name      string
		n         int
		wantPanic bool
	}{
		{"negative", -1, true},
		{"zero", 0, true},
		{"one", 1, false},
		{"positive", 5, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if tt.wantPanic && r == nil {
					t.Errorf("NewSlidingWindow(%d): expected panic, got none", tt.n)
				}
				if !tt.wantPanic && r != nil {
					t.Errorf("NewSlidingWindow(%d): unexpected panic: %v", tt.n, r)
				}
			}()
			_ = compactor.NewSlidingWindow(tt.n)
		})
	}
}

func TestSlidingWindowReturnsIndependentCopy(t *testing.T) {
	c := compactor.NewSlidingWindow(2)
	msgs := []gantry.Message{{Content: "a"}, {Content: "b"}, {Content: "c"}}
	got, err := c.Compact(context.Background(), msgs, compactor.Budget{})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	// got is the last 2 (== msgs[1:]); mutating it must not corrupt the input.
	got[0].Content = "MUTATED"
	if msgs[1].Content != "b" {
		t.Errorf("Compact aliased input: msgs[1] = %q, want \"b\"", msgs[1].Content)
	}
}

package compactor_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/compactor"
)

func TestHeadTailKeepsHeadAndTail(t *testing.T) {
	c := compactor.NewHeadTail(1, 2)
	msgs := []gantry.Message{
		{Content: "h1"}, {Content: "mid1"}, {Content: "mid2"}, {Content: "t1"}, {Content: "t2"},
	}
	got, err := c.Compact(context.Background(), msgs, compactor.Budget{})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	want := []string{"h1", "t1", "t2"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (got %+v)", len(got), len(want), got)
	}
	for i, c := range got {
		if c.Content != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, c.Content, want[i])
		}
	}
}

func TestNewHeadTailValidatesArgs(t *testing.T) {
	tests := []struct {
		name       string
		head, tail int
		wantPanic  bool
	}{
		{"negative head", -1, 2, true},
		{"negative tail", 2, -1, true},
		{"both negative", -1, -1, true},
		{"both zero", 0, 0, true},
		{"head zero tail positive", 0, 2, false},
		{"head positive tail zero", 2, 0, false},
		{"both positive", 1, 2, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if tt.wantPanic && r == nil {
					t.Errorf("NewHeadTail(%d, %d): expected panic, got none", tt.head, tt.tail)
				}
				if !tt.wantPanic && r != nil {
					t.Errorf("NewHeadTail(%d, %d): unexpected panic: %v", tt.head, tt.tail, r)
				}
			}()
			_ = compactor.NewHeadTail(tt.head, tt.tail)
		})
	}
}

func TestHeadTailNoTrimmingWhenSmallEnough(t *testing.T) {
	c := compactor.NewHeadTail(2, 2)
	msgs := []gantry.Message{{Content: "a"}, {Content: "b"}, {Content: "c"}}
	got, _ := c.Compact(context.Background(), msgs, compactor.Budget{})
	if len(got) != 3 {
		t.Errorf("len = %d, want 3", len(got))
	}
}

func TestHeadTailPassThroughReturnsIndependentCopy(t *testing.T) {
	c := compactor.NewHeadTail(2, 2) // head+tail = 4 >= len(msgs) = 3 -> pass-through
	msgs := []gantry.Message{{Content: "a"}, {Content: "b"}, {Content: "c"}}
	got, err := c.Compact(context.Background(), msgs, compactor.Budget{})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (pass-through)", len(got))
	}
	got[0].Content = "MUTATED"
	if msgs[0].Content != "a" {
		t.Errorf("pass-through aliased input: msgs[0] = %q, want \"a\"", msgs[0].Content)
	}
}

package humanloop_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/components/humanloop"
)

func TestNoOpApproves(t *testing.T) {
	h := humanloop.NewNoOp()
	d, err := h.Confirm(context.Background(), humanloop.Action{Kind: "tool", Name: "anything"})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if !d.Approved {
		t.Errorf("NoOp should approve; got %+v", d)
	}
}

func TestAutoApprover(t *testing.T) {
	h := humanloop.NewAutoApprover()
	d, _ := h.Confirm(context.Background(), humanloop.Action{Kind: "tool"})
	if !d.Approved {
		t.Errorf("AutoApprover should approve")
	}
}

func TestAutoDenier(t *testing.T) {
	h := humanloop.NewAutoDenier("blocked by policy")
	d, _ := h.Confirm(context.Background(), humanloop.Action{Kind: "tool"})
	if d.Approved {
		t.Errorf("AutoDenier should not approve")
	}
	if d.Reason != "blocked by policy" {
		t.Errorf("Reason = %q", d.Reason)
	}
}

package task

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
)

// scriptedReplanner is a fake Replanner recording every invocation. It returns
// the configured plan (or error) on each call.
type scriptedReplanner struct {
	calls   int
	reasons []string
	plan    *gantry.Plan
	err     error
}

func (r *scriptedReplanner) Replan(_ context.Context, _ *Task, reason string) (*gantry.Plan, error) {
	r.calls++
	r.reasons = append(r.reasons, reason)
	if r.err != nil {
		return nil, r.err
	}
	return r.plan, nil
}

// Reference the imports until Task 4's tests use them, so this file compiles
// standalone after Task 3.
var _ = errors.New
var _ = strings.Contains

func TestWithReplannerSetsSeam(t *testing.T) {
	rp := &scriptedReplanner{}
	d := NewDriver(&scriptedRunner{}, NewInMemory(), WithReplanner(rp))
	if d.replanner != rp {
		t.Errorf("d.replanner = %v, want the injected replanner", d.replanner)
	}
}

func TestWithReplannerNilKeepsDefault(t *testing.T) {
	d := NewDriver(&scriptedRunner{}, NewInMemory(), WithReplanner(nil))
	if d.replanner != nil {
		t.Errorf("d.replanner = %v, want nil (no replanning by default)", d.replanner)
	}
}

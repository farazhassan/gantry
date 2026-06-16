package gantry_test

import (
	"testing"

	"github.com/farazhassan/gantry"
)

func TestNewStateInitsCollections(t *testing.T) {
	s := gantry.NewState("input")
	if s.Input != "input" {
		t.Errorf("Input = %q", s.Input)
	}
	if s.Meta == nil {
		t.Errorf("Meta is nil; should be initialized")
	}
	if s.Trace == nil {
		t.Errorf("Trace is nil; should be initialized")
	}
	if s.Done {
		t.Errorf("new state should not be Done")
	}
	if s.Iteration != 0 {
		t.Errorf("Iteration = %d, want 0", s.Iteration)
	}
}

func TestStateSetGetMeta(t *testing.T) {
	s := gantry.NewState("x")
	s.Meta["key"] = "value"
	if s.Meta["key"] != "value" {
		t.Errorf("Meta round-trip failed")
	}
}

package gantry_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/farazhassan/gantry"
)

func TestPendingResultErrorsAsThroughWrap(t *testing.T) {
	orig := &gantry.PendingResult{
		Pending: []gantry.ToolCall{{ID: "leaf1", Name: "ask_user", Input: []byte(`{}`)}},
		Resume:  []byte(`{"step":1}`),
	}
	wrapped := fmt.Errorf("wrap: %w", orig)

	var got *gantry.PendingResult
	if !errors.As(wrapped, &got) {
		t.Fatal("errors.As did not find *PendingResult through a wrap")
	}
	if got != orig {
		t.Errorf("got %p, want the same pointer as orig %p", got, orig)
	}
}

func TestPendingResultErrorTextIsNonEmpty(t *testing.T) {
	var err error = &gantry.PendingResult{}
	if err.Error() == "" {
		t.Error("Error() returned an empty string")
	}
}

package main

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
)

func TestSessionExample(t *testing.T) {
	res, err := RunExample(context.Background())
	if err != nil {
		t.Fatalf("RunExample returned error: %v", err)
	}

	// Turn 1 seeds the transcript: [user, assistant].
	if len(res.Turn1.Messages) != 2 {
		t.Errorf("turn 1 len(Messages) = %d, want 2", len(res.Turn1.Messages))
	}

	// Turn 2 carries turn 1 forward (continuity).
	if len(res.Turn2.Messages) != 4 {
		t.Errorf("turn 2 len(Messages) = %d, want 4", len(res.Turn2.Messages))
	}
	if res.Turn2.Messages[0].Content != "my name is Faraz." {
		t.Errorf("turn 2 first message = %q, want turn 1's input", res.Turn2.Messages[0].Content)
	}
	if res.Turn2.Usage.InputTokens != 20 {
		t.Errorf("turn 2 Usage.InputTokens = %d, want 20 (cumulative)", res.Turn2.Usage.InputTokens)
	}

	// Resumed turn (second Manager, same store) sees the whole history.
	if len(res.Resumed.Messages) != 6 {
		t.Errorf("resumed len(Messages) = %d, want 6", len(res.Resumed.Messages))
	}
	if res.Resumed.Usage.InputTokens != 30 {
		t.Errorf("resumed Usage.InputTokens = %d, want 30 (cumulative across managers)", res.Resumed.Usage.InputTokens)
	}
	if res.Resumed.DoneReason != gantry.DoneNoToolCalls {
		t.Errorf("resumed DoneReason = %q, want %q", res.Resumed.DoneReason, gantry.DoneNoToolCalls)
	}
}

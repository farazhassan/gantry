package eval_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

func TestRecordingLLMClientCapturesRequestsAndResponses(t *testing.T) {
	upstream := eval.NewMockLLMClient(
		harness.LLMResponse{Content: "first"},
		harness.LLMResponse{Content: "second"},
	)
	rec := eval.NewRecordingLLMClient(upstream)
	ctx := context.Background()

	r1, err := rec.Generate(ctx, harness.LLMRequest{System: "s1"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if r1.Content != "first" {
		t.Errorf("content = %q", r1.Content)
	}
	_, _ = rec.Generate(ctx, harness.LLMRequest{System: "s2"})

	turns := rec.Recording()
	if len(turns) != 2 {
		t.Fatalf("recording len = %d, want 2", len(turns))
	}
	if turns[0].Request.System != "s1" || turns[0].Response.Content != "first" {
		t.Errorf("turn[0] = %+v", turns[0])
	}
}

func TestRecordingLLMClientWriteAndLoad(t *testing.T) {
	upstream := eval.NewMockLLMClient(harness.LLMResponse{Content: "x"})
	rec := eval.NewRecordingLLMClient(upstream)
	_, _ = rec.Generate(context.Background(), harness.LLMRequest{System: "sys"})

	var buf bytes.Buffer
	if err := rec.WriteJSONL(&buf); err != nil {
		t.Fatalf("WriteJSONL: %v", err)
	}
	turns, err := eval.LoadRecording(&buf)
	if err != nil {
		t.Fatalf("LoadRecording: %v", err)
	}
	if len(turns) != 1 || turns[0].Request.System != "sys" {
		t.Errorf("loaded turns = %+v", turns)
	}
}

func TestReplayLLMClientReplaysInOrder(t *testing.T) {
	turns := []eval.RecordedTurn{
		{Request: harness.LLMRequest{}, Response: harness.LLMResponse{Content: "a"}},
		{Request: harness.LLMRequest{}, Response: harness.LLMResponse{Content: "b"}},
	}
	rp := eval.NewReplayLLMClient(turns)
	r1, _ := rp.Generate(context.Background(), harness.LLMRequest{})
	r2, _ := rp.Generate(context.Background(), harness.LLMRequest{})
	if r1.Content != "a" || r2.Content != "b" {
		t.Errorf("got %q, %q", r1.Content, r2.Content)
	}
}

func TestReplayLLMClientExhausted(t *testing.T) {
	rp := eval.NewReplayLLMClient(nil)
	_, err := rp.Generate(context.Background(), harness.LLMRequest{})
	if !errors.Is(err, eval.ErrReplayExhausted) {
		t.Errorf("err = %v, want ErrReplayExhausted", err)
	}
}

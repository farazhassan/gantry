package eval_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

func TestMockLLMClientScriptedResponses(t *testing.T) {
	m := eval.NewMockLLMClient(
		gantry.LLMResponse{Content: "first", StopReason: gantry.StopReasonEnd},
		gantry.LLMResponse{Content: "second", StopReason: gantry.StopReasonEnd},
	)
	ctx := context.Background()

	r1, err := m.Generate(ctx, gantry.LLMRequest{})
	if err != nil {
		t.Fatalf("call 1: %v", err)
	}
	if r1.Content != "first" {
		t.Errorf("call 1 Content = %q", r1.Content)
	}

	r2, err := m.Generate(ctx, gantry.LLMRequest{})
	if err != nil {
		t.Fatalf("call 2: %v", err)
	}
	if r2.Content != "second" {
		t.Errorf("call 2 Content = %q", r2.Content)
	}
}

func TestMockLLMClientExhaustedReturnsError(t *testing.T) {
	m := eval.NewMockLLMClient(gantry.LLMResponse{Content: "only"})
	_, _ = m.Generate(context.Background(), gantry.LLMRequest{})
	_, err := m.Generate(context.Background(), gantry.LLMRequest{})
	if err == nil {
		t.Fatalf("expected error on second call; got nil")
	}
	if !errors.Is(err, eval.ErrMockExhausted) {
		t.Errorf("err = %v, want ErrMockExhausted", err)
	}
}

func TestMockLLMClientReset(t *testing.T) {
	m := eval.NewMockLLMClient(
		gantry.LLMResponse{Content: "first", StopReason: gantry.StopReasonEnd},
		gantry.LLMResponse{Content: "second", StopReason: gantry.StopReasonEnd},
	)
	ctx := context.Background()
	_, _ = m.Generate(ctx, gantry.LLMRequest{})
	_, _ = m.Generate(ctx, gantry.LLMRequest{})
	m.Reset()
	if len(m.Requests()) != 0 {
		t.Errorf("after Reset, Requests() should be empty; got %d", len(m.Requests()))
	}
	r, err := m.Generate(ctx, gantry.LLMRequest{})
	if err != nil {
		t.Fatalf("after Reset, Generate err: %v", err)
	}
	if r.Content != "first" {
		t.Errorf("after Reset, first call Content = %q, want %q", r.Content, "first")
	}
}

func TestMockLLMClientRecordsRequests(t *testing.T) {
	m := eval.NewMockLLMClient(gantry.LLMResponse{Content: "x"})
	req := gantry.LLMRequest{System: "you are helpful", Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}}}
	_, _ = m.Generate(context.Background(), req)
	got := m.Requests()
	if len(got) != 1 {
		t.Fatalf("Requests() len = %d, want 1", len(got))
	}
	if got[0].System != "you are helpful" {
		t.Errorf("recorded system mismatch: %q", got[0].System)
	}
}

func TestMockLLMClientWithError(t *testing.T) {
	wantErr := errors.New("scripted")
	m := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Err: wantErr},
	})
	_, err := m.Generate(context.Background(), gantry.LLMRequest{})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}

package eval

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/farazhassan/gantry/harness"
)

func TestGenerateStreamReconstructsContent(t *testing.T) {
	want := "hello streaming world"
	m := NewMockLLMClient(harness.LLMResponse{Content: want, StopReason: harness.StopReasonEnd})

	var sb strings.Builder
	var sawStop harness.StopReason
	resp, err := m.GenerateStream(context.Background(), harness.LLMRequest{}, func(ch harness.StreamChunk) error {
		sb.WriteString(ch.TextDelta)
		if ch.StopReason != "" {
			sawStop = ch.StopReason
		}
		return nil
	})
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	if sb.String() != want {
		t.Errorf("reconstructed deltas = %q, want %q", sb.String(), want)
	}
	if resp.Content != want {
		t.Errorf("final resp.Content = %q, want %q", resp.Content, want)
	}
	if sawStop != harness.StopReasonEnd {
		t.Errorf("final chunk StopReason = %q, want %q", sawStop, harness.StopReasonEnd)
	}
}

func TestGenerateStreamYieldErrorPropagates(t *testing.T) {
	m := NewMockLLMClient(harness.LLMResponse{Content: "abcdefghijkl", StopReason: harness.StopReasonEnd})
	sentinel := errors.New("consumer boom")

	_, err := m.GenerateStream(context.Background(), harness.LLMRequest{}, func(harness.StreamChunk) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want %v", err, sentinel)
	}
}

func TestGenerateStreamExhausted(t *testing.T) {
	m := NewMockLLMClient() // empty script
	_, err := m.GenerateStream(context.Background(), harness.LLMRequest{}, func(harness.StreamChunk) error { return nil })
	if !errors.Is(err, ErrMockExhausted) {
		t.Errorf("err = %v, want ErrMockExhausted", err)
	}
}

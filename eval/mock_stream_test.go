package eval

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
)

func TestGenerateStreamReconstructsContent(t *testing.T) {
	want := "hello streaming world"
	m := NewMockLLMClient(gantry.LLMResponse{Content: want, StopReason: gantry.StopReasonEnd})

	var sb strings.Builder
	var sawStop gantry.StopReason
	resp, err := m.GenerateStream(context.Background(), gantry.LLMRequest{}, func(ch gantry.StreamChunk) error {
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
	if sawStop != gantry.StopReasonEnd {
		t.Errorf("final chunk StopReason = %q, want %q", sawStop, gantry.StopReasonEnd)
	}
}

func TestGenerateStreamYieldErrorPropagates(t *testing.T) {
	m := NewMockLLMClient(gantry.LLMResponse{Content: "abcdefghijkl", StopReason: gantry.StopReasonEnd})
	sentinel := errors.New("consumer boom")

	_, err := m.GenerateStream(context.Background(), gantry.LLMRequest{}, func(gantry.StreamChunk) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want %v", err, sentinel)
	}
}

func TestGenerateStreamExhausted(t *testing.T) {
	m := NewMockLLMClient() // empty script
	_, err := m.GenerateStream(context.Background(), gantry.LLMRequest{}, func(gantry.StreamChunk) error { return nil })
	if !errors.Is(err, ErrMockExhausted) {
		t.Errorf("err = %v, want ErrMockExhausted", err)
	}
}

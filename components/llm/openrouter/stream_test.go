package openrouter_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
)

// sse writes lines as a Server-Sent Events body (each event is "data: <p>\n\n").
func sse(w http.ResponseWriter, payloads ...string) {
	for _, p := range payloads {
		_, _ = io.WriteString(w, "data: "+p+"\n\n")
	}
}

func TestGenerateStreamAggregatesDeltas(t *testing.T) {
	var gotStream any
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = decodeJSON(r, &body)
		gotStream = body["stream"]
		sse(w,
			`{"choices":[{"delta":{"role":"assistant","content":"Hel"}}]}`,
			`{"choices":[{"delta":{"content":"lo "}}]}`,
			`{"choices":[{"delta":{"content":"world"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3}}`,
			"[DONE]",
		)
	})

	var deltas []string
	resp, err := c.GenerateStream(context.Background(), gantry.LLMRequest{
		Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}},
	}, func(ch gantry.StreamChunk) error {
		if ch.TextDelta != "" {
			deltas = append(deltas, ch.TextDelta)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	if gotStream != true {
		t.Errorf("stream flag = %v, want true", gotStream)
	}
	if got := strings.Join(deltas, ""); got != "Hello world" {
		t.Errorf("concatenated deltas = %q, want %q", got, "Hello world")
	}
	if resp.Content != "Hello world" {
		t.Errorf("resp.Content = %q, want %q", resp.Content, "Hello world")
	}
	if resp.StopReason != gantry.StopReasonEnd {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, gantry.StopReasonEnd)
	}
	if resp.Usage.InputTokens != 5 || resp.Usage.OutputTokens != 3 {
		t.Errorf("Usage = %+v, want In=5 Out=3", resp.Usage)
	}
}

func TestGenerateStreamYieldsTerminalUsageChunk(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			`{"choices":[{"delta":{"content":"hi"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1}}`,
			"[DONE]",
		)
	})

	var terminal *gantry.StreamChunk
	_, err := c.GenerateStream(context.Background(), gantry.LLMRequest{
		Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}},
	}, func(ch gantry.StreamChunk) error {
		if ch.TextDelta == "" {
			cp := ch
			terminal = &cp
		}
		return nil
	})
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	if terminal == nil {
		t.Fatal("expected a terminal (empty-delta) chunk carrying StopReason + Usage")
	}
	if terminal.StopReason != gantry.StopReasonEnd {
		t.Errorf("terminal StopReason = %q, want %q", terminal.StopReason, gantry.StopReasonEnd)
	}
	if terminal.Usage == nil || terminal.Usage.OutputTokens != 1 {
		t.Errorf("terminal Usage = %+v, want OutputTokens=1", terminal.Usage)
	}
}

func TestGenerateStreamAccumulatesToolCallFragments(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"calc","arguments":"{\"a\":"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"2}"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			"[DONE]",
		)
	})

	resp, err := c.GenerateStream(context.Background(), gantry.LLMRequest{
		Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "calc"}},
	}, func(gantry.StreamChunk) error { return nil })
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %+v, want 1", resp.ToolCalls)
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "calc" {
		t.Errorf("ToolCall = %+v, want id=call_1 name=calc", tc)
	}
	if string(tc.Input) != `{"a":2}` {
		t.Errorf("ToolCall.Input = %s, want {\"a\":2}", tc.Input)
	}
	if resp.StopReason != gantry.StopReasonToolUse {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, gantry.StopReasonToolUse)
	}
}

func TestGenerateStreamYieldErrorPropagates(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			`{"choices":[{"delta":{"content":"one"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			"[DONE]",
		)
	})

	boom := errors.New("yield boom")
	_, err := c.GenerateStream(context.Background(), gantry.LLMRequest{
		Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}},
	}, func(gantry.StreamChunk) error { return boom })
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want yield error propagated", err)
	}
}

func TestGenerateStreamPropagatesContextCancellation(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"choices":[{"delta":{"content":"x"},"finish_reason":"stop"}]}`, "[DONE]")
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.GenerateStream(ctx, gantry.LLMRequest{
		Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "x"}},
	}, func(gantry.StreamChunk) error { return nil })
	if err == nil {
		t.Error("want error on canceled context, got nil")
	}
}

package ollama_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/farazhassan/gantry/harness"
)

// ndjson writes lines as a streamed NDJSON body.
func ndjson(w http.ResponseWriter, lines ...string) {
	for _, l := range lines {
		_, _ = io.WriteString(w, l+"\n")
	}
}

func TestGenerateStreamAggregatesDeltas(t *testing.T) {
	var gotStream any
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = decodeJSON(r, &body)
		gotStream = body["stream"]
		ndjson(w,
			`{"message":{"role":"assistant","content":"Hel"},"done":false}`,
			`{"message":{"role":"assistant","content":"lo "},"done":false}`,
			`{"message":{"role":"assistant","content":"world"},"done":false}`,
			`{"message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":3}`,
		)
	})

	var deltas []string
	resp, err := c.GenerateStream(context.Background(), harness.LLMRequest{
		Messages: []harness.Message{{Role: harness.RoleUser, Content: "hi"}},
	}, func(ch harness.StreamChunk) error {
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
	if resp.StopReason != harness.StopReasonEnd {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, harness.StopReasonEnd)
	}
	if resp.Usage.InputTokens != 5 || resp.Usage.OutputTokens != 3 {
		t.Errorf("Usage = %+v, want In=5 Out=3", resp.Usage)
	}
}

func TestGenerateStreamYieldsTerminalUsageChunk(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		ndjson(w,
			`{"message":{"content":"hi"},"done":false}`,
			`{"message":{"content":""},"done":true,"done_reason":"stop","prompt_eval_count":2,"eval_count":1}`,
		)
	})

	var terminal *harness.StreamChunk
	_, err := c.GenerateStream(context.Background(), harness.LLMRequest{
		Messages: []harness.Message{{Role: harness.RoleUser, Content: "hi"}},
	}, func(ch harness.StreamChunk) error {
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
	if terminal.StopReason != harness.StopReasonEnd {
		t.Errorf("terminal StopReason = %q, want %q", terminal.StopReason, harness.StopReasonEnd)
	}
	if terminal.Usage == nil || terminal.Usage.OutputTokens != 1 {
		t.Errorf("terminal Usage = %+v, want OutputTokens=1", terminal.Usage)
	}
}

func TestGenerateStreamToolCalls(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		ndjson(w,
			`{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"calc","arguments":{"a":2}}}]},"done":false}`,
			`{"message":{"content":""},"done":true,"done_reason":"stop"}`,
		)
	})

	resp, err := c.GenerateStream(context.Background(), harness.LLMRequest{
		Messages: []harness.Message{{Role: harness.RoleUser, Content: "calc"}},
	}, func(harness.StreamChunk) error { return nil })
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "calc" || resp.ToolCalls[0].ID != "call-0" {
		t.Fatalf("ToolCalls = %+v, want one call-0/calc", resp.ToolCalls)
	}
	if resp.StopReason != harness.StopReasonToolUse {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, harness.StopReasonToolUse)
	}
}

func TestGenerateStreamYieldErrorPropagates(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		ndjson(w,
			`{"message":{"content":"one"},"done":false}`,
			`{"message":{"content":"two"},"done":true,"done_reason":"stop"}`,
		)
	})

	boom := errors.New("yield boom")
	_, err := c.GenerateStream(context.Background(), harness.LLMRequest{
		Messages: []harness.Message{{Role: harness.RoleUser, Content: "hi"}},
	}, func(harness.StreamChunk) error { return boom })
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want yield error propagated", err)
	}
}

func TestGenerateStreamPropagatesContextCancellation(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		ndjson(w, `{"message":{"content":"x"},"done":true}`)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.GenerateStream(ctx, harness.LLMRequest{
		Messages: []harness.Message{{Role: harness.RoleUser, Content: "x"}},
	}, func(harness.StreamChunk) error { return nil })
	if err == nil {
		t.Error("want error on canceled context, got nil")
	}
}

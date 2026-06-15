package anthropic_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/farazhassan/gantry/harness"
)

// sse writes Anthropic-style Server-Sent Events: an "event:" line followed by a
// "data:" line for each payload. The client parses the data lines (which carry
// a "type" field), so the event name is cosmetic but included for realism.
func sse(w http.ResponseWriter, events [][2]string) {
	for _, ev := range events {
		_, _ = io.WriteString(w, "event: "+ev[0]+"\n")
		_, _ = io.WriteString(w, "data: "+ev[1]+"\n\n")
	}
}

func TestGenerateStreamAggregatesDeltas(t *testing.T) {
	var gotStream any
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = decodeJSON(r, &body)
		gotStream = body["stream"]
		sse(w, [][2]string{
			{"message_start", `{"type":"message_start","message":{"usage":{"input_tokens":5,"output_tokens":0}}}`},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo "}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"world"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`},
			{"message_stop", `{"type":"message_stop"}`},
		})
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
		sse(w, [][2]string{
			{"message_start", `{"type":"message_start","message":{"usage":{"input_tokens":2,"output_tokens":0}}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`},
			{"message_stop", `{"type":"message_stop"}`},
		})
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

func TestGenerateStreamAccumulatesToolInputFragments(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w, [][2]string{
			{"message_start", `{"type":"message_start","message":{"usage":{"input_tokens":4,"output_tokens":0}}}`},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"calc","input":{}}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"2}"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":9}}`},
			{"message_stop", `{"type":"message_stop"}`},
		})
	})

	resp, err := c.GenerateStream(context.Background(), harness.LLMRequest{
		Messages: []harness.Message{{Role: harness.RoleUser, Content: "calc"}},
	}, func(harness.StreamChunk) error { return nil })
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %+v, want 1", resp.ToolCalls)
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "toolu_1" || tc.Name != "calc" {
		t.Errorf("ToolCall = %+v, want id=toolu_1 name=calc", tc)
	}
	if string(tc.Input) != `{"a":2}` {
		t.Errorf("ToolCall.Input = %s, want {\"a\":2}", tc.Input)
	}
	if resp.StopReason != harness.StopReasonToolUse {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, harness.StopReasonToolUse)
	}
}

func TestGenerateStreamYieldErrorPropagates(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w, [][2]string{
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"one"}}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`},
			{"message_stop", `{"type":"message_stop"}`},
		})
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
		sse(w, [][2]string{
			{"message_stop", `{"type":"message_stop"}`},
		})
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

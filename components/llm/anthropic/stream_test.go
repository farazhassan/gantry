package anthropic_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/llm/anthropic"
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

func TestGenerateStreamYieldsReasoningDeltas(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w, [][2]string{
			{"message_start", `{"type":"message_start","message":{"usage":{"input_tokens":3,"output_tokens":0}}}`},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me "}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"think..."}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"answer"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":1}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`},
			{"message_stop", `{"type":"message_stop"}`},
		})
	})

	var reasoning, text []string
	resp, err := c.GenerateStream(context.Background(), gantry.LLMRequest{
		Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}},
	}, func(ch gantry.StreamChunk) error {
		if ch.ReasoningDelta != "" {
			reasoning = append(reasoning, ch.ReasoningDelta)
		}
		if ch.TextDelta != "" {
			text = append(text, ch.TextDelta)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	if got := strings.Join(reasoning, ""); got != "Let me think..." {
		t.Errorf("reasoning deltas = %q, want %q", got, "Let me think...")
	}
	if got := strings.Join(text, ""); got != "answer" {
		t.Errorf("text deltas = %q, want %q", got, "answer")
	}
	if resp.Content != "answer" {
		t.Errorf("resp.Content = %q, want %q (thinking block excluded from content)", resp.Content, "answer")
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
	if tc.ID != "toolu_1" || tc.Name != "calc" {
		t.Errorf("ToolCall = %+v, want id=toolu_1 name=calc", tc)
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
		sse(w, [][2]string{
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"one"}}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`},
			{"message_stop", `{"type":"message_stop"}`},
		})
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
		sse(w, [][2]string{
			{"message_stop", `{"type":"message_stop"}`},
		})
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

func TestGenerateStreamYieldsRawFrameForUnhandledEventTypes(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w, [][2]string{
			{"ping", `{"type":"ping"}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"message_stop", `{"type":"message_stop"}`},
		})
	})

	var raw []gantry.StreamChunk
	_, err := c.GenerateStream(context.Background(), gantry.LLMRequest{
		Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}},
	}, func(ch gantry.StreamChunk) error {
		if len(ch.RawFrame) > 0 {
			raw = append(raw, ch)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	if len(raw) != 2 {
		t.Fatalf("raw frames = %d, want 2 (ping + content_block_stop): %+v", len(raw), raw)
	}
	for _, ch := range raw {
		if ch.RawSource != "anthropic" {
			t.Errorf("RawSource = %q, want %q", ch.RawSource, "anthropic")
		}
	}
	if !strings.Contains(string(raw[0].RawFrame), `"type":"ping"`) {
		t.Errorf("raw[0] = %s, want the ping frame", raw[0].RawFrame)
	}
	if !strings.Contains(string(raw[1].RawFrame), `"content_block_stop"`) {
		t.Errorf("raw[1] = %s, want the content_block_stop frame", raw[1].RawFrame)
	}
}

func TestGenerateStreamRawFrameSurvivesBufferReuse(t *testing.T) {
	// A ping frame near the start, then enough subsequent SSE traffic to force
	// bufio.Scanner's internal buffer to grow/shift/reuse memory, proving
	// RawFrame was copied out of the scanner's buffer rather than aliasing it
	// (see the fix in GenerateStream's default case).
	events := [][2]string{
		{"ping", `{"type":"ping","marker":"original-ping-content"}`},
	}
	// Pad with enough large content_block_delta events to exceed the
	// scanner's growth thresholds (64KB initial, grows toward 1MiB).
	filler := strings.Repeat("x", 4096)
	for i := 0; i < 40; i++ {
		events = append(events, [2]string{
			"content_block_delta",
			fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, filler),
		})
	}
	events = append(events, [2]string{"message_stop", `{"type":"message_stop"}`})

	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w, events)
	})

	var capturedRaw json.RawMessage
	_, err := c.GenerateStream(context.Background(), gantry.LLMRequest{
		Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}},
	}, func(ch gantry.StreamChunk) error {
		if len(ch.RawFrame) > 0 && capturedRaw == nil {
			capturedRaw = ch.RawFrame // held WITHOUT copying by the test, on purpose
		}
		return nil
	})
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	if capturedRaw == nil {
		t.Fatal("no raw frame captured")
	}
	// Checked only NOW, after the scanner has processed >150KB of subsequent
	// data — if GenerateStream aliased the scanner's buffer instead of
	// copying, this content would now be corrupted/different.
	if !strings.Contains(string(capturedRaw), "original-ping-content") {
		t.Errorf("captured RawFrame corrupted after subsequent scanning: %s", capturedRaw)
	}
}

func TestGenerateStreamSendsThinkingConfigWhenEnabled(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = decodeJSON(r, &gotBody)
		sse(w, [][2]string{
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`},
			{"message_stop", `{"type":"message_stop"}`},
		})
	}))
	defer srv.Close()
	c := anthropic.New("test-model",
		anthropic.WithAPIKey("test-key"),
		anthropic.WithBaseURL(srv.URL),
		anthropic.WithHTTPClient(srv.Client()),
		anthropic.WithExtendedThinking(1024),
	)

	_, err := c.GenerateStream(context.Background(), gantry.LLMRequest{
		Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}},
	}, func(gantry.StreamChunk) error { return nil })
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}

	thinking, ok := gotBody["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("request body missing \"thinking\" object: %+v", gotBody)
	}
	if thinking["type"] != "enabled" {
		t.Errorf("thinking.type = %v, want %q", thinking["type"], "enabled")
	}
	if budget, ok := thinking["budget_tokens"].(float64); !ok || int(budget) != 1024 {
		t.Errorf("thinking.budget_tokens = %v, want 1024", thinking["budget_tokens"])
	}
}

func TestGenerateStreamOmitsThinkingConfigByDefault(t *testing.T) {
	var gotBody map[string]any
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = decodeJSON(r, &gotBody)
		sse(w, [][2]string{
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`},
			{"message_stop", `{"type":"message_stop"}`},
		})
	})
	_, err := c.GenerateStream(context.Background(), gantry.LLMRequest{
		Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}},
	}, func(gantry.StreamChunk) error { return nil })
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	if _, ok := gotBody["thinking"]; ok {
		t.Errorf("request body has \"thinking\" key = %v, want omitted (extended thinking not configured)", gotBody["thinking"])
	}
}

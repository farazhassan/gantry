package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
)

func TestStreamHandlerEmitsSSEEvents(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	rec := httptest.NewRecorder() // implements http.Flusher

	streamHandler(rec, req)

	res := rec.Result()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	var events []gantry.Event
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev gantry.Event
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatalf("unmarshal event %q: %v", line, err)
		}
		events = append(events, ev)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(events) == 0 {
		t.Fatal("no events parsed from SSE stream")
	}
	last := events[len(events)-1]
	if last.Type != gantry.EventDone {
		t.Errorf("last event = %q, want done", last.Type)
	}
	if last.FinalOutput != "2 + 3 = 5 (computed by the calc tool)." {
		t.Errorf("final output = %q", last.FinalOutput)
	}

	var sawDelta, sawToolCall, sawToolResult bool
	for _, ev := range events {
		switch ev.Type {
		case gantry.EventTextDelta:
			sawDelta = true
		case gantry.EventToolCall:
			sawToolCall = true
		case gantry.EventToolResult:
			sawToolResult = true
		}
	}
	if !sawDelta {
		t.Error("expected at least one text_delta event")
	}
	if !sawToolCall {
		t.Error("expected a tool_call event")
	}
	if !sawToolResult {
		t.Error("expected a tool_result event")
	}
}

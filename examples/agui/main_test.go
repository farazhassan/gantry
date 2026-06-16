package main

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

// TestServerStreamsRunToFinish exercises the example's handler wiring with a
// scripted mock LLM, so it stays hermetic with respect to any LLM provider: it
// proves the server emits a well-formed AG-UI SSE stream ending in RUN_FINISHED.
func TestServerStreamsRunToFinish(t *testing.T) {
	llm := eval.NewMockLLMClient(gantry.LLMResponse{
		Content:    "Hi there friend.",
		StopReason: gantry.StopReasonEnd,
	})

	handler, err := newHandler(llm)
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}

	srv := httptest.NewServer(handler)
	defer srv.Close()

	body := `{"messages":[{"role":"user","content":"Say hi."}]}`
	resp, err := srv.Client().Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	var sb strings.Builder
	if _, err := io.Copy(&sb, resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	got := sb.String()

	for _, want := range []string{"RUN_STARTED", "TEXT_MESSAGE_START", "RUN_FINISHED"} {
		if !strings.Contains(got, want) {
			t.Errorf("SSE stream missing %q\nfull stream:\n%s", want, got)
		}
	}
}

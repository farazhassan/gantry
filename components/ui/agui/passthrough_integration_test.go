package agui

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/subagent"
	"github.com/farazhassan/gantry/eval"
)

// rawFrame is enough of the AG-UI wire shape to assert on in this test
// without importing every concrete Event type.
type rawFrame struct {
	Type             string `json:"type"`
	RunID            string `json:"runId"`
	Agent            string `json:"agent"`
	ParentToolCallID string `json:"parentToolCallId"`
	StepName         string `json:"stepName"`
	Name             string `json:"name"`
	SubagentRunID    string `json:"subagentRunId"`
	Message          string `json:"message"`
}

func TestPassthroughTwoConcurrentSubagentsEndToEnd(t *testing.T) {
	investigation, err := gantry.NewAgent(
		gantry.WithLLM(eval.NewMockLLMClient(gantry.LLMResponse{Content: "root cause found", StopReason: gantry.StopReasonEnd})),
		gantry.WithName("investigation"),
	)
	if err != nil {
		t.Fatalf("NewAgent(investigation): %v", err)
	}
	strategy, err := gantry.NewAgent(
		gantry.WithLLM(eval.NewMockLLMClient(gantry.LLMResponse{Content: "recommended fix", StopReason: gantry.StopReasonEnd})),
		gantry.WithName("strategy"),
	)
	if err != nil {
		t.Fatalf("NewAgent(strategy): %v", err)
	}

	investigateTool := subagent.New("investigate", "d", investigation, subagent.WithEventPassthrough())
	strategizeTool := subagent.New("strategize", "d", strategy, subagent.WithEventPassthrough())

	orchestratorMock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "call-investigate", Name: "investigate", Input: json.RawMessage(`{"goal":"g"}`)},
				{ID: "call-strategize", Name: "strategize", Input: json.RawMessage(`{"goal":"g"}`)},
			},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "closing summary", StopReason: gantry.StopReasonEnd},
	)
	orchestrator, err := gantry.NewAgent(
		gantry.WithLLM(orchestratorMock),
		gantry.WithName("orchestrator"),
		gantry.WithComponents(subagent.Component(0, investigateTool, strategizeTool)), // parallelism=0: full parallelism, both dispatch at once
	)
	if err != nil {
		t.Fatalf("NewAgent(orchestrator): %v", err)
	}

	srv := httptest.NewServer(Handler(orchestrator))
	defer srv.Close()

	body := `{"threadId":"t1","runId":"r1","messages":[{"id":"m1","role":"user","content":"why is conversion down?"}]}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var frames []rawFrame
	runFinishedCount := 0
	investigationSteps, strategySteps := 0, 0
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var f rawFrame
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &f); err != nil {
			t.Fatalf("unmarshal frame %q: %v", line, err)
		}
		frames = append(frames, f)
		switch f.Type {
		case "RUN_FINISHED":
			runFinishedCount++
		case "STEP_STARTED":
			switch f.Agent {
			case "investigation":
				investigationSteps++
				if f.ParentToolCallID != "call-investigate" {
					t.Errorf("investigation STEP_STARTED parentToolCallId = %q, want call-investigate", f.ParentToolCallID)
				}
			case "strategy":
				strategySteps++
				if f.ParentToolCallID != "call-strategize" {
					t.Errorf("strategy STEP_STARTED parentToolCallId = %q, want call-strategize", f.ParentToolCallID)
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(frames) == 0 {
		t.Fatal("no SSE frames received")
	}
	if runFinishedCount != 1 {
		t.Errorf("RUN_FINISHED count = %d, want exactly 1 (a nested run finishing must not end the stream)", runFinishedCount)
	}
	if investigationSteps == 0 {
		t.Error("expected investigation's own STEP_STARTED events on the stream, got none")
	}
	if strategySteps == 0 {
		t.Error("expected strategy's own STEP_STARTED events on the stream, got none")
	}
	if frames[len(frames)-1].Type != "RUN_FINISHED" {
		t.Errorf("last frame type = %q, want RUN_FINISHED (must be the terminal frame)", frames[len(frames)-1].Type)
	}
}

func TestPassthroughSubagentStartedThenFinishedBracket(t *testing.T) {
	investigation, err := gantry.NewAgent(
		gantry.WithLLM(eval.NewMockLLMClient(gantry.LLMResponse{Content: "root cause found", StopReason: gantry.StopReasonEnd})),
		gantry.WithName("investigation"),
	)
	if err != nil {
		t.Fatalf("NewAgent(investigation): %v", err)
	}
	investigateTool := subagent.New("researcher", "Delegates fact-finding", investigation, subagent.WithEventPassthrough())

	orchestratorMock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "call-investigate", Name: "researcher", Input: json.RawMessage(`{"goal":"g"}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "closing summary", StopReason: gantry.StopReasonEnd},
	)
	orchestrator, err := gantry.NewAgent(
		gantry.WithLLM(orchestratorMock),
		gantry.WithName("orchestrator"),
		gantry.WithComponents(subagent.Component(1, investigateTool)),
	)
	if err != nil {
		t.Fatalf("NewAgent(orchestrator): %v", err)
	}

	srv := httptest.NewServer(Handler(orchestrator))
	defer srv.Close()

	body := `{"threadId":"t1","runId":"r1","messages":[{"id":"m1","role":"user","content":"why is conversion down?"}]}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var frames []rawFrame
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var f rawFrame
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &f); err != nil {
			t.Fatalf("unmarshal frame %q: %v", line, err)
		}
		frames = append(frames, f)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	startIdx, finishIdx := -1, -1
	var subagentRunID string
	for i, f := range frames {
		switch f.Type {
		case "SUBAGENT_STARTED":
			startIdx = i
			subagentRunID = f.SubagentRunID
			if f.Name != "researcher" {
				t.Errorf("SUBAGENT_STARTED name = %q, want researcher", f.Name)
			}
			if f.SubagentRunID == "" {
				t.Error("SUBAGENT_STARTED subagentRunId is empty, want the child's RunID")
			}
		case "SUBAGENT_FINISHED":
			finishIdx = i
			if f.SubagentRunID != subagentRunID {
				t.Errorf("SUBAGENT_FINISHED subagentRunId = %q, want %q (must match SUBAGENT_STARTED)", f.SubagentRunID, subagentRunID)
			}
		}
	}
	if startIdx == -1 {
		t.Fatal("expected a SUBAGENT_STARTED frame, got none")
	}
	if finishIdx == -1 {
		t.Fatal("expected a SUBAGENT_FINISHED frame, got none")
	}
	if finishIdx <= startIdx {
		t.Errorf("SUBAGENT_FINISHED at index %d, want after SUBAGENT_STARTED at index %d", finishIdx, startIdx)
	}
}

// A child whose LLM call fails aborts with an error the parent recovers from
// (dispatch hands it to the parent's model as an error tool result). The
// child's SUBAGENT_STARTED must still be closed by a SUBAGENT_ERROR before
// RUN_FINISHED, or an AG-UI client's verifier rejects the stream.
func TestPassthroughSubagentLLMErrorClosesWithSubagentError(t *testing.T) {
	critic, err := gantry.NewAgent(gantry.WithLLM(erroringLLM{}), gantry.WithName("critic"))
	if err != nil {
		t.Fatalf("NewAgent(critic): %v", err)
	}
	critiqueTool := subagent.New("critique", "d", critic, subagent.WithEventPassthrough())
	orchestrator, err := gantry.NewAgent(
		gantry.WithLLM(eval.NewMockLLMClient(
			gantry.LLMResponse{
				ToolCalls:  []gantry.ToolCall{{ID: "call-critique", Name: "critique", Input: json.RawMessage(`{"goal":"g"}`)}},
				StopReason: gantry.StopReasonToolUse,
			},
			gantry.LLMResponse{Content: "carried on without the critique", StopReason: gantry.StopReasonEnd},
		)),
		gantry.WithName("orchestrator"),
		gantry.WithComponents(subagent.Component(0, critiqueTool)),
	)
	if err != nil {
		t.Fatalf("NewAgent(orchestrator): %v", err)
	}

	srv := httptest.NewServer(Handler(orchestrator))
	defer srv.Close()
	body := `{"threadId":"t1","runId":"r1","messages":[{"id":"m1","role":"user","content":"go"}]}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	var types []string
	var started, errored []rawFrame
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var f rawFrame
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &f); err != nil {
			t.Fatalf("unmarshal frame %q: %v", line, err)
		}
		types = append(types, f.Type)
		switch f.Type {
		case "SUBAGENT_STARTED":
			started = append(started, f)
		case "SUBAGENT_ERROR":
			errored = append(errored, f)
		case "SUBAGENT_FINISHED":
			t.Errorf("got SUBAGENT_FINISHED for a child whose run failed")
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(started) != 1 || len(errored) != 1 {
		t.Fatalf("SUBAGENT_STARTED = %d, SUBAGENT_ERROR = %d, want 1 each; frames: %v", len(started), len(errored), types)
	}
	if errored[0].SubagentRunID != started[0].SubagentRunID {
		t.Errorf("SUBAGENT_ERROR subagentRunId = %q, want the started one %q", errored[0].SubagentRunID, started[0].SubagentRunID)
	}
	if !strings.Contains(errored[0].Message, "llm boom") {
		t.Errorf("SUBAGENT_ERROR message = %q, want it to carry the LLM error", errored[0].Message)
	}
	if types[len(types)-1] != "RUN_FINISHED" {
		t.Errorf("last frame = %q, want RUN_FINISHED; frames: %v", types[len(types)-1], types)
	}
}

package gantry_test

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

// collectEvents runs the supplied run function with a recording sink and
// returns every event it emitted, failing the test on error or zero events.
func collectEvents(t *testing.T, run func(sink gantry.EventSink) (*gantry.State, error)) []gantry.Event {
	t.Helper()
	var events []gantry.Event
	if _, err := run(func(ev gantry.Event) error {
		events = append(events, ev)
		return nil
	}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected events, got none")
	}
	return events
}

func isHex(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return len(s) > 0
}

func TestRunStreamStampsRunIDAndAgentOnEveryEvent(t *testing.T) {
	a, err := gantry.NewAgent(
		gantry.WithLLM(eval.NewMockLLMClient(
			gantry.LLMResponse{Content: "hello world", StopReason: gantry.StopReasonEnd},
		)),
		gantry.WithName("writer"),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	events := collectEvents(t, func(sink gantry.EventSink) (*gantry.State, error) {
		return a.RunStream(context.Background(), "go", sink)
	})

	runID := events[0].RunID
	if !strings.HasPrefix(runID, "run-") || !isHex(strings.TrimPrefix(runID, "run-")) || len(runID) != len("run-")+16 {
		t.Fatalf("RunID = %q, want \"run-\" plus 16 hex chars (8 random bytes)", runID)
	}
	var sawTextDelta bool
	for i, ev := range events {
		if ev.RunID != runID {
			t.Errorf("event %d RunID = %q, want %q (one id per run)", i, ev.RunID, runID)
		}
		if ev.Agent != "writer" {
			t.Errorf("event %d Agent = %q, want writer", i, ev.Agent)
		}
		if ev.SessionID != "" || ev.TaskID != "" {
			t.Errorf("event %d session/task = %q/%q, want empty (no task meta)", i, ev.SessionID, ev.TaskID)
		}
		if ev.Type == gantry.EventTextDelta {
			sawTextDelta = true
		}
	}
	if !sawTextDelta {
		t.Fatal("expected streamed text_delta events (identity must cover the streaming emit path)")
	}
}

func TestRunIDDiffersAcrossRuns(t *testing.T) {
	newAgent := func() *gantry.Agent {
		a, err := gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient(
			gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
		)))
		if err != nil {
			t.Fatalf("NewAgent: %v", err)
		}
		return a
	}
	first := collectEvents(t, func(sink gantry.EventSink) (*gantry.State, error) {
		return newAgent().RunStream(context.Background(), "one", sink)
	})
	second := collectEvents(t, func(sink gantry.EventSink) (*gantry.State, error) {
		return newAgent().RunStream(context.Background(), "two", sink)
	})
	if first[0].RunID == second[0].RunID {
		t.Errorf("two runs share RunID %q, want distinct ids", first[0].RunID)
	}
}

func TestRunFoldsParentLinkIntoNestedIdentity(t *testing.T) {
	a, err := gantry.NewAgent(
		gantry.WithLLM(eval.NewMockLLMClient(
			gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
		)),
		gantry.WithName("investigation"),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx := gantry.WithParentLink(context.Background(), gantry.ParentLink{
		RunID:      "run-parent-123",
		ToolCallID: "call-1",
	})

	events := collectEvents(t, func(sink gantry.EventSink) (*gantry.State, error) {
		return a.RunStream(ctx, "investigate", sink)
	})

	for i, ev := range events {
		if ev.ParentRunID != "run-parent-123" {
			t.Errorf("event %d ParentRunID = %q, want run-parent-123", i, ev.ParentRunID)
		}
		if ev.ParentToolCallID != "call-1" {
			t.Errorf("event %d ParentToolCallID = %q, want call-1", i, ev.ParentToolCallID)
		}
	}
}

func TestRunWithoutParentLinkLeavesParentFieldsEmpty(t *testing.T) {
	a, err := gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient(
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	events := collectEvents(t, func(sink gantry.EventSink) (*gantry.State, error) {
		return a.RunStream(context.Background(), "go", sink)
	})
	for i, ev := range events {
		if ev.ParentRunID != "" || ev.ParentToolCallID != "" {
			t.Errorf("event %d parent fields = %q/%q, want empty (top-level run)", i, ev.ParentRunID, ev.ParentToolCallID)
		}
	}
}

func TestSessionAndTaskIDStampedFromStateMeta(t *testing.T) {
	a, err := gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient(
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	prior := gantry.NewState("do the thing")
	prior.Meta[gantry.MetaSessionID] = "sess-42"
	prior.Meta[gantry.MetaTaskID] = "task-7"

	events := collectEvents(t, func(sink gantry.EventSink) (*gantry.State, error) {
		return a.ResumeStream(context.Background(), prior, sink)
	})
	for i, ev := range events {
		if ev.SessionID != "sess-42" || ev.TaskID != "task-7" {
			t.Errorf("event %d identity = session %q / task %q, want sess-42 / task-7",
				i, ev.SessionID, ev.TaskID)
		}
	}
}

func TestNonStringMetaIdentityIsIgnored(t *testing.T) {
	a, err := gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient(
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	prior := gantry.NewState("go")
	prior.Meta[gantry.MetaSessionID] = 42 // wrong type: must not panic, must not stamp
	prior.Meta[gantry.MetaTaskID] = struct{}{}

	events := collectEvents(t, func(sink gantry.EventSink) (*gantry.State, error) {
		return a.ResumeStream(context.Background(), prior, sink)
	})
	for i, ev := range events {
		if ev.SessionID != "" || ev.TaskID != "" {
			t.Errorf("event %d identity = %q/%q, want empty for non-string meta", i, ev.SessionID, ev.TaskID)
		}
	}
}

func TestRunSpanCarriesIdentityAttrs(t *testing.T) {
	a, err := gantry.NewAgent(
		gantry.WithLLM(eval.NewMockLLMClient(
			gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
		)),
		gantry.WithName("writer"),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	prior := gantry.NewState("go")
	prior.Meta[gantry.MetaSessionID] = "sess-42"
	prior.Meta[gantry.MetaTaskID] = "task-7"

	state, err := a.Resume(context.Background(), prior)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}

	var runEnd *gantry.TraceEvent
	for _, ev := range state.Trace.Snapshot() {
		if ev.Kind == gantry.KindSpanEnd && ev.Name == "run" {
			e := ev
			runEnd = &e
			break
		}
	}
	if runEnd == nil {
		t.Fatal("no ended run span in trace")
	}
	runID, _ := runEnd.Attrs["run.id"].(string)
	if !strings.HasPrefix(runID, "run-") {
		t.Errorf("run span run.id = %v, want a run- prefixed id", runEnd.Attrs["run.id"])
	}
	if runEnd.Attrs["agent.name"] != "writer" {
		t.Errorf("run span agent.name = %v, want writer", runEnd.Attrs["agent.name"])
	}
	if runEnd.Attrs["session.id"] != "sess-42" || runEnd.Attrs["task.id"] != "task-7" {
		t.Errorf("run span session/task attrs = %v / %v, want sess-42 / task-7",
			runEnd.Attrs["session.id"], runEnd.Attrs["task.id"])
	}
}

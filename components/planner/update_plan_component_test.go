package planner_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/planner"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/eval"
)

// seedPlanState returns a Resume-able state carrying a hydrated-style plan,
// the way task.Driver seeds runs (task/driver.go): Plan set, transcript empty.
func seedPlanState(input string) *gantry.State {
	st := gantry.NewState(input)
	st.Plan = &gantry.Plan{
		Goal: "ship it",
		Steps: []gantry.PlanStep{
			{ID: "s1", Description: "design", Status: gantry.StepActive, AcceptanceCriteria: "spec approved"},
		},
	}
	return st
}

func updatePlanCall(id string) gantry.ToolCall {
	return gantry.ToolCall{
		ID:   id,
		Name: "update_plan",
		Input: json.RawMessage(`{"ops":[` +
			`{"op":"set_status","step_id":"s1","status":"done"},` +
			`{"op":"add_step","description":"write docs","acceptance_criteria":["README updated"]}]}`),
	}
}

func requestHasTool(req gantry.LLMRequest, name string) bool {
	return countToolDefs(req, name) > 0
}

func countToolDefs(req gantry.LLMRequest, name string) int {
	n := 0
	for _, d := range req.Tools {
		if d.Name == name {
			n++
		}
	}
	return n
}

func TestUpdatePlanInterceptionMutatesStatePlan(t *testing.T) {
	llm := eval.NewMockLLMClient(
		gantry.LLMResponse{ToolCalls: []gantry.ToolCall{updatePlanCall("c1")}, StopReason: gantry.StopReasonToolUse},
		gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd},
	)
	a, err := gantry.NewAgent(gantry.WithLLM(llm))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if err := a.With(planner.UpdatePlan()); err != nil {
		t.Fatalf("install: %v", err)
	}

	out, err := a.Resume(context.Background(), seedPlanState("work the plan"))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if out.Plan.Steps[0].Status != gantry.StepDone {
		t.Errorf("s1 status = %q, want done", out.Plan.Steps[0].Status)
	}
	if len(out.Plan.Steps) != 2 {
		t.Fatalf("steps = %d, want 2 (add_step appended)", len(out.Plan.Steps))
	}
	added := out.Plan.Steps[1]
	if added.ID != "s2" || added.Description != "write docs" || added.Status != gantry.StepPending {
		t.Errorf("added step = %+v, want (s2, write docs, pending)", added)
	}
	if added.AcceptanceCriteria != "README updated" {
		t.Errorf("criteria = %q, want README updated", added.AcceptanceCriteria)
	}
	// The synthesized result reached the transcript as an ordinary tool message.
	var toolMsg gantry.Message
	for _, m := range out.Messages {
		if m.Role == gantry.RoleTool && m.ToolCallID == "c1" {
			toolMsg = m
		}
	}
	if toolMsg.Content == "" {
		t.Fatalf("no tool message for c1 in transcript: %+v", out.Messages)
	}
	if !strings.Contains(toolMsg.Content, `"ok":true`) || !strings.Contains(toolMsg.Content, `"step_id":"s2"`) {
		t.Errorf("result content = %q, want per-op ok results incl. minted id s2", toolMsg.Content)
	}
	// The tool was advertised because a plan existed.
	if !requestHasTool(llm.Requests()[0], "update_plan") {
		t.Errorf("update_plan def missing from first LLM request tools")
	}
	// Advertised once, not once per iteration.
	if n := countToolDefs(llm.Requests()[1], "update_plan"); n != 1 {
		t.Errorf("second request has %d update_plan defs, want 1", n)
	}
}

func TestUpdatePlanNotAdvertisedWithoutPlan(t *testing.T) {
	llm := eval.NewMockLLMClient(gantry.LLMResponse{Content: "hi", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(llm))
	if err := a.With(planner.UpdatePlan()); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := a.Run(context.Background(), "just chat"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requestHasTool(llm.Requests()[0], "update_plan") {
		t.Errorf("update_plan advertised on a planless run")
	}
}

func TestUpdatePlanUnknownStepIDIsPerOpError(t *testing.T) {
	call := gantry.ToolCall{ID: "c1", Name: "update_plan",
		Input: json.RawMessage(`{"ops":[{"op":"set_status","step_id":"ghost","status":"done"}]}`)}
	llm := eval.NewMockLLMClient(
		gantry.LLMResponse{ToolCalls: []gantry.ToolCall{call}, StopReason: gantry.StopReasonToolUse},
		gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(llm))
	if err := a.With(planner.UpdatePlan()); err != nil {
		t.Fatalf("install: %v", err)
	}
	out, err := a.Resume(context.Background(), seedPlanState("go"))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if out.Plan.Steps[0].Status != gantry.StepActive {
		t.Errorf("plan mutated by a failed op: %+v", out.Plan.Steps[0])
	}
	var content string
	for _, m := range out.Messages {
		if m.Role == gantry.RoleTool && m.ToolCallID == "c1" {
			content = m.Content
		}
	}
	if !strings.Contains(content, "unknown step_id") || !strings.Contains(content, "ghost") {
		t.Errorf("result = %q, want a per-op unknown step_id error", content)
	}
}

// echoTool is a minimal registered tool for the coexistence tests.
type echoTool struct{}

func (echoTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{Name: "echo", Description: "echoes its input", Schema: json.RawMessage(`{"type":"object"}`)}
}

func (echoTool) Invoke(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
	return input, nil
}

func TestUpdatePlanCoexistsWithDispatch(t *testing.T) {
	calls := []gantry.ToolCall{
		{ID: "c1", Name: "echo", Input: json.RawMessage(`{"say":"hi"}`)},
		updatePlanCall("c2"),
	}
	llm := eval.NewMockLLMClient(
		gantry.LLMResponse{ToolCalls: calls, StopReason: gantry.StopReasonToolUse},
		gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(llm))
	// Tool dispatch first, UpdatePlan second: registration is innermost-first,
	// so the interception composes outermost and runs BEFORE dispatch.
	if err := a.With(tool.FromTools(1, echoTool{}), planner.UpdatePlan()); err != nil {
		t.Fatalf("install: %v", err)
	}
	out, err := a.Resume(context.Background(), seedPlanState("go"))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	assertInterceptedAlongsideEcho(t, out)
}

func TestUpdatePlanInstalledBeforeDispatchStillIntercepts(t *testing.T) {
	calls := []gantry.ToolCall{
		{ID: "c1", Name: "echo", Input: json.RawMessage(`{"say":"hi"}`)},
		updatePlanCall("c2"),
	}
	llm := eval.NewMockLLMClient(
		gantry.LLMResponse{ToolCalls: calls, StopReason: gantry.StopReasonToolUse},
		gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(llm))
	// Reversed install order: dispatch runs first and records an "unknown tool"
	// error result for c2; the interception (running after) must replace it via
	// upsert rather than append a duplicate.
	if err := a.With(planner.UpdatePlan(), tool.FromTools(1, echoTool{})); err != nil {
		t.Fatalf("install: %v", err)
	}
	out, err := a.Resume(context.Background(), seedPlanState("go"))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	assertInterceptedAlongsideEcho(t, out)
}

// assertInterceptedAlongsideEcho asserts the shared outcome of the two
// coexistence tests: echo dispatched normally, update_plan applied to the
// plan, and exactly one clean tool result for the update_plan call.
func assertInterceptedAlongsideEcho(t *testing.T, out *gantry.State) {
	t.Helper()
	if out.Plan.Steps[0].Status != gantry.StepDone || len(out.Plan.Steps) != 2 {
		t.Errorf("plan not mutated by interception: %+v", out.Plan)
	}
	var echoContent string
	var updateMsgs []gantry.Message
	for _, m := range out.Messages {
		if m.Role != gantry.RoleTool {
			continue
		}
		switch m.ToolCallID {
		case "c1":
			echoContent = m.Content
		case "c2":
			updateMsgs = append(updateMsgs, m)
		}
	}
	if !strings.Contains(echoContent, "hi") {
		t.Errorf("echo result = %q, want the echoed input", echoContent)
	}
	if len(updateMsgs) != 1 {
		t.Fatalf("got %d tool messages for c2, want exactly 1 (upsert, no duplicates)", len(updateMsgs))
	}
	if strings.Contains(updateMsgs[0].Content, "unknown tool") {
		t.Errorf("dispatch's unknown-tool error leaked into the transcript: %q", updateMsgs[0].Content)
	}
	if !strings.Contains(updateMsgs[0].Content, `"results"`) {
		t.Errorf("c2 result = %q, want the synthesized results payload", updateMsgs[0].Content)
	}
}

func TestUpdatePlanInterceptionEmitsPlanStepChanged(t *testing.T) {
	llm := eval.NewMockLLMClient(
		gantry.LLMResponse{ToolCalls: []gantry.ToolCall{updatePlanCall("c1")}, StopReason: gantry.StopReasonToolUse},
		gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd},
	)
	a, err := gantry.NewAgent(gantry.WithLLM(llm))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if err := a.With(planner.UpdatePlan()); err != nil {
		t.Fatalf("install: %v", err)
	}

	var changed []gantry.PlanStep
	_, err = a.ResumeStream(context.Background(), seedPlanState("work the plan"), func(ev gantry.Event) error {
		if ev.Type == gantry.EventPlanStepChanged && ev.PlanStep != nil {
			changed = append(changed, *ev.PlanStep)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ResumeStream: %v", err)
	}
	// updatePlanCall applies set_status(s1, done) and add_step("write docs") —
	// both are successful ops with a StepID, so both must emit.
	if len(changed) != 2 {
		t.Fatalf("changed = %+v, want 2 EventPlanStepChanged events", changed)
	}
	if changed[0].ID != "s1" || changed[0].Status != gantry.StepDone {
		t.Errorf("changed[0] = %+v, want s1/done", changed[0])
	}
	if changed[1].Description != "write docs" || changed[1].Status != gantry.StepPending {
		t.Errorf("changed[1] = %+v, want the newly added step", changed[1])
	}
}

func TestUpdatePlanInstallTwiceErrors(t *testing.T) {
	llm := eval.NewMockLLMClient()
	a, _ := gantry.NewAgent(gantry.WithLLM(llm))
	if err := a.With(planner.UpdatePlan()); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if err := a.With(planner.UpdatePlan()); err == nil {
		t.Fatalf("second install must error")
	}
}

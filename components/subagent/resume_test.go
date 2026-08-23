// components/subagent/resume_test.go
package subagent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/eval"
)

// TestResumeEntryPointFoldsResumeLegChildUsage is the real-bug regression
// test: it exercises subagent.Resume exactly the way an application actually
// calls it — a plain context.Background(), no manually pre-installed
// usageRecorder — mirroring the genuine call pattern of tool.Resume's own
// documented usage (and this package's end-to-end nested-resume tests), not
// the hand-wired pattern TestResumeFoldsOnlyIncrementalUsageNotCumulative
// uses to unit-test delegateTool.Resume in isolation.
//
// Before the fix, calling tool.Resume directly (or delegateTool.Resume with
// no recorder in ctx) from ordinary application code silently dropped the
// resumed child's resume-leg usage: usageRecorderFrom found nothing, so the
// delta computed in delegateTool.Resume had nowhere to be recorded and the
// child's post-resume tokens never reached the parent's state.Usage at all.
func TestResumeEntryPointFoldsResumeLegChildUsage(t *testing.T) {
	childMock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "ask1", Name: "ask_user", Input: json.RawMessage(`{"q":"proceed?"}`)}},
			StopReason: gantry.StopReasonToolUse,
			Usage:      gantry.Usage{InputTokens: 7, OutputTokens: 11},
		},
		gantry.LLMResponse{
			Content:    "resumed answer",
			StopReason: gantry.StopReasonEnd,
			Usage:      gantry.Usage{InputTokens: 5, OutputTokens: 9},
		},
	)
	child, err := gantry.NewAgent(gantry.WithLLM(childMock))
	if err != nil {
		t.Fatalf("NewAgent(child): %v", err)
	}
	if err := child.With(tool.Client(gantry.ToolDef{Name: "ask_user", Description: "d", Schema: json.RawMessage(`{}`)})); err != nil {
		t.Fatalf("install client tools on child: %v", err)
	}
	delegate := New("planner", "plans, asking before executing", child)

	parentMock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "planner", Input: json.RawMessage(`{"goal":"ship it"}`)}},
			StopReason: gantry.StopReasonToolUse,
			Usage:      gantry.Usage{InputTokens: 2, OutputTokens: 3},
		},
		gantry.LLMResponse{
			Content:    "the planner reports: resumed answer",
			StopReason: gantry.StopReasonEnd,
			Usage:      gantry.Usage{InputTokens: 1, OutputTokens: 1},
		},
	)
	parentComp, parentReg := ComponentWithRegistry(1, delegate)
	parent, err := gantry.NewAgent(gantry.WithLLM(parentMock), gantry.WithComponents(parentComp))
	if err != nil {
		t.Fatalf("NewAgent(parent): %v", err)
	}

	suspended, err := parent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !suspended.Done || suspended.DoneReason != gantry.DoneClientToolCall {
		t.Fatalf("parent Done=%v DoneReason=%q, want suspended by the child's ask_user call", suspended.Done, suspended.DoneReason)
	}
	wantSuspendedUsage := gantry.Usage{InputTokens: 9, OutputTokens: 14} // parent turn1 (2,3) + child pre-suspend turn (7,11)
	if suspended.Usage != wantSuspendedUsage {
		t.Fatalf("suspended.Usage = %+v, want %+v", suspended.Usage, wantSuspendedUsage)
	}

	// The real application entry point: plain context, the new package-level
	// Resume — NOT a hand-installed recorder, NOT tool.Resume directly.
	final, err := Resume(context.Background(), parent, parentReg, suspended, []gantry.ToolResult{
		{CallID: suspended.PendingToolCalls[0].ID, Content: `{"answer":"yes"}`},
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if final.DoneReason != gantry.DoneNoToolCalls || final.FinalOutput != "the planner reports: resumed answer" {
		t.Fatalf("Done=%q out=%q, want the parent to finish normally using the planner's completed output", final.DoneReason, final.FinalOutput)
	}

	// The crux of the bug: the resume leg's child usage (5,9) must be folded
	// in on top of the pre-suspend usage (9,14) and the parent's own
	// continuation turn (1,1).
	wantFinalUsage := gantry.Usage{InputTokens: 15, OutputTokens: 24}
	if final.Usage != wantFinalUsage {
		t.Errorf("final.Usage = %+v, want %+v (resume-leg child usage must be folded into the parent's state.Usage)", final.Usage, wantFinalUsage)
	}
}

// TestResumeEntryPointFoldsUsageAcrossTwoNestedLevels proves the
// multi-level-nesting correctness property the fix depends on: the single
// usageRecorder installed once by the top-level Resume call must be visible
// to — and correctly accumulate deltas from — a delegateTool.Resume at BOTH
// the child level and the grandchild level, reached through a second, INNER
// tool.Resume call issued from inside the child's own delegateTool.Resume.
// If context values were ever stripped along that path (withDepth,
// gantry.WithoutSink, context.WithTimeout), the grandchild's and/or child's
// resume-leg usage would silently vanish exactly like the original bug, just
// one level deeper.
func TestResumeEntryPointFoldsUsageAcrossTwoNestedLevels(t *testing.T) {
	grandchildMock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "ask1", Name: "ask_user", Input: json.RawMessage(`{"q":"deep question?"}`)}},
			StopReason: gantry.StopReasonToolUse,
			Usage:      gantry.Usage{InputTokens: 13, OutputTokens: 14},
		},
		gantry.LLMResponse{
			Content:    "grandchild done",
			StopReason: gantry.StopReasonEnd,
			Usage:      gantry.Usage{InputTokens: 15, OutputTokens: 16},
		},
	)
	grandchild, err := gantry.NewAgent(gantry.WithLLM(grandchildMock))
	if err != nil {
		t.Fatalf("NewAgent(grandchild): %v", err)
	}
	if err := grandchild.With(tool.Client(gantry.ToolDef{Name: "ask_user", Description: "d", Schema: json.RawMessage(`{}`)})); err != nil {
		t.Fatalf("install client tools on grandchild: %v", err)
	}
	deepDelegate := New("deep_specialist", "d", grandchild)

	childMock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "g1", Name: "deep_specialist", Input: json.RawMessage(`{"goal":"dig deeper"}`)}},
			StopReason: gantry.StopReasonToolUse,
			Usage:      gantry.Usage{InputTokens: 9, OutputTokens: 10},
		},
		gantry.LLMResponse{
			Content:    "child used the grandchild's answer",
			StopReason: gantry.StopReasonEnd,
			Usage:      gantry.Usage{InputTokens: 11, OutputTokens: 12},
		},
	)
	childComp, childReg := ComponentWithRegistry(1, deepDelegate)
	child, err := gantry.NewAgent(gantry.WithLLM(childMock), gantry.WithComponents(childComp))
	if err != nil {
		t.Fatalf("NewAgent(child): %v", err)
	}
	delegate := New("specialist", "d", child, WithChildRegistry(childReg))

	parentMock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "specialist", Input: json.RawMessage(`{"goal":"go deep"}`)}},
			StopReason: gantry.StopReasonToolUse,
			Usage:      gantry.Usage{InputTokens: 5, OutputTokens: 6},
		},
		gantry.LLMResponse{
			Content:    "parent finished",
			StopReason: gantry.StopReasonEnd,
			Usage:      gantry.Usage{InputTokens: 7, OutputTokens: 8},
		},
	)
	parentComp, parentReg := ComponentWithRegistry(1, delegate)
	parent, err := gantry.NewAgent(gantry.WithLLM(parentMock), gantry.WithComponents(parentComp))
	if err != nil {
		t.Fatalf("NewAgent(parent): %v", err)
	}

	suspended, err := parent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !suspended.Done || suspended.DoneReason != gantry.DoneClientToolCall {
		t.Fatalf("Done=%v DoneReason=%q, want suspended by the grandchild's ask_user call", suspended.Done, suspended.DoneReason)
	}
	// Pre-suspend usage: only each level's first turn has run so far.
	wantSuspendedUsage := gantry.Usage{InputTokens: 5 + 9 + 13, OutputTokens: 6 + 10 + 14}
	if suspended.Usage != wantSuspendedUsage {
		t.Fatalf("suspended.Usage = %+v, want %+v", suspended.Usage, wantSuspendedUsage)
	}

	final, err := Resume(context.Background(), parent, parentReg, suspended, []gantry.ToolResult{
		{CallID: suspended.PendingToolCalls[0].ID, Content: `{"answer":"the deep answer"}`},
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if final.DoneReason != gantry.DoneNoToolCalls || final.FinalOutput != "parent finished" {
		t.Fatalf("Done=%q out=%q, want the parent to finish normally after resuming through two nested levels", final.DoneReason, final.FinalOutput)
	}

	// Every turn at every level, counted exactly once: parent's two turns,
	// child's two turns, and grandchild's two turns.
	wantFinalUsage := gantry.Usage{
		InputTokens:  5 + 7 + 9 + 11 + 13 + 15,
		OutputTokens: 6 + 8 + 10 + 12 + 14 + 16,
	}
	if final.Usage != wantFinalUsage {
		t.Errorf("final.Usage = %+v, want %+v (both the child's and grandchild's resume-leg usage must reach the top-level parent's state.Usage)", final.Usage, wantFinalUsage)
	}
}

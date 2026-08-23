// components/subagent/dynamic_client_resume_test.go
package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/eval"
)

// TestResumeReinstallsChildDynamicClientToolsAcrossSuspend is the regression
// test for the gap flagged in GitHub PR #80 review comment 3836269229
// (components/subagent/subagent.go:239): DynamicClient's PhaseStart
// middleware consumes and deletes SetPendingClientTools' per-run defs the
// moment it reads them, so a child *gantry.State marshaled at suspend and
// later unmarshaled by delegateTool.Resume has nothing left in state.Meta
// for a second PhaseStart pass to reinstall. If the resumed child's next LLM
// turn asks for another of its own dynamic client tools — one it isn't told
// about again after the very first turn, since nothing in the nested-resume
// path calls SetPendingClientTools a second time — it must still be
// recognized and marked client-side (causing a clean re-suspend) rather than
// silently mis-routed as an ordinary, unregistered tool call.
func TestResumeReinstallsChildDynamicClientToolsAcrossSuspend(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			// Turn 1: only get_location is called; get_weather, though
			// advertised in the very same defs set, goes unused this turn.
			ToolCalls:  []gantry.ToolCall{{ID: "q1", Name: "get_location", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{
			// Turn 2, after resume: the child's model asks for the OTHER
			// dynamic tool. Without the fix, DynamicClient's PhaseStart has
			// nothing left in state.Meta to reinstall this turn, so
			// get_weather is neither advertised nor marked client-side.
			ToolCalls:  []gantry.ToolCall{{ID: "q2", Name: "get_weather", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
	)
	child, err := gantry.NewAgent(gantry.WithLLM(mock))
	if err != nil {
		t.Fatalf("NewAgent(child): %v", err)
	}
	if err := child.With(tool.DynamicClient()); err != nil {
		t.Fatalf("install dynamic client on child: %v", err)
	}

	// Seed the child's very first run with both dynamic defs, the way a
	// caller driving DynamicClient must (see SetPendingClientTools's doc
	// comment) — mirroring the pattern components/tool's own DynamicClient
	// tests use (e.g. TestDynamicClientSuspendsOnPerRunTool). Getting an
	// initial per-invocation def set into a delegated child's first run is a
	// separate, Invoke-side gap from the one under test here, which is
	// scoped to delegateTool.Resume carrying them across a suspend (PR #80
	// review comment 3836269229).
	prior := &gantry.State{}
	if err := tool.SetPendingClientTools(prior,
		gantry.ToolDef{Name: "get_location", Description: "d", Schema: json.RawMessage(`{}`)},
		gantry.ToolDef{Name: "get_weather", Description: "d", Schema: json.RawMessage(`{}`)},
	); err != nil {
		t.Fatalf("SetPendingClientTools: %v", err)
	}

	suspended, err := child.RunFrom(context.Background(), prior, "find my location and the weather")
	if err != nil {
		t.Fatalf("RunFrom: %v", err)
	}
	if !suspended.Done || suspended.DoneReason != gantry.DoneClientToolCall {
		t.Fatalf("Done=%v DoneReason=%q, want suspend on get_location", suspended.Done, suspended.DoneReason)
	}

	// delegateTool.asResult's own marshal step, reproduced here since
	// `suspended` was built directly via RunFrom rather than through Invoke.
	marshaled, err := json.Marshal(suspended)
	if err != nil {
		t.Fatalf("marshal suspended child state: %v", err)
	}

	tl := New("assistant", "d", child)
	resumable, ok := tl.(tool.ResumableTool)
	if !ok {
		t.Fatal("delegate tool does not implement tool.ResumableTool")
	}

	_, err = resumable.Resume(context.Background(), marshaled, []gantry.ToolResult{
		{CallID: "q1", Content: `{"lat":0,"lng":0}`},
	})
	var pending *gantry.PendingResult
	if !errors.As(err, &pending) {
		t.Fatalf("Resume err = %v, want a *gantry.PendingResult (the child suspending again on get_weather)", err)
	}
	if len(pending.Pending) != 1 || pending.Pending[0].Name != "get_weather" {
		t.Fatalf("Pending = %#v, want exactly the child's get_weather call, correctly marked client-side and surfaced", pending.Pending)
	}
}

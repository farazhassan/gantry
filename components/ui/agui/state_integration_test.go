package agui

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/subagent"
	"github.com/farazhassan/gantry/eval"
)

// stateFrame is enough of the AG-UI wire shape to find STATE_SNAPSHOT frames
// and their payloads.
type stateFrame struct {
	Type     string         `json:"type"`
	Snapshot map[string]any `json:"snapshot"`
}

// setStateAtStart installs a PhaseStart middleware on a that publishes
// snapshot via gantry.SetState once per run.
func setStateAtStart(a *gantry.Agent, snapshot map[string]any) {
	a.Use(gantry.PhaseStart, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if err := gantry.SetState(ctx, snapshot); err != nil {
				return err
			}
			return next(ctx, s)
		}
	})
}

func TestStateSnapshotEndToEndResetsThenDropsSubagentState(t *testing.T) {
	child, err := gantry.NewAgent(
		gantry.WithLLM(eval.NewMockLLMClient(gantry.LLMResponse{Content: "child done", StopReason: gantry.StopReasonEnd})),
		gantry.WithName("investigation"),
	)
	if err != nil {
		t.Fatalf("NewAgent(child): %v", err)
	}
	setStateAtStart(child, map[string]any{"owner": "investigation"})

	parent, err := gantry.NewAgent(
		gantry.WithLLM(eval.NewMockLLMClient(
			gantry.LLMResponse{
				ToolCalls:  []gantry.ToolCall{{ID: "call-investigate", Name: "investigate", Input: json.RawMessage(`{"goal":"g"}`)}},
				StopReason: gantry.StopReasonToolUse,
			},
			gantry.LLMResponse{Content: "summary", StopReason: gantry.StopReasonEnd},
		)),
		gantry.WithName("orchestrator"),
		gantry.WithComponents(subagent.Component(1, subagent.New("investigate", "d", child, subagent.WithEventPassthrough()))),
	)
	if err != nil {
		t.Fatalf("NewAgent(parent): %v", err)
	}
	setStateAtStart(parent, map[string]any{"owner": "orchestrator"})

	var buf bytes.Buffer
	s := NewSink(&buf, "t1", "r1", WithRunStateReset())
	if _, err := parent.RunStream(context.Background(), "go", s.Sink()); err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	var types []string
	var states []stateFrame
	for _, line := range strings.Split(buf.String(), "\n") {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var f stateFrame
		if err := json.Unmarshal([]byte(payload), &f); err != nil {
			t.Fatalf("unmarshal %q: %v", payload, err)
		}
		types = append(types, f.Type)
		if f.Type == "STATE_SNAPSHOT" {
			states = append(states, f)
		}
	}

	if len(types) < 2 || types[0] != "RUN_STARTED" || types[1] != "STATE_SNAPSHOT" {
		t.Fatalf("first frames = %v, want RUN_STARTED then the reset STATE_SNAPSHOT", types)
	}
	if !slices.Contains(types, "SUBAGENT_STARTED") {
		t.Fatalf("no SUBAGENT_STARTED in %v: the child never streamed, so the drop wasn't exercised", types)
	}
	if len(states) != 2 {
		t.Fatalf("got %d STATE_SNAPSHOT frames %+v, want 2 (reset + orchestrator; the sub-agent's dropped)", len(states), states)
	}
	if len(states[0].Snapshot) != 0 {
		t.Errorf("reset snapshot = %v, want {}", states[0].Snapshot)
	}
	if states[1].Snapshot["owner"] != "orchestrator" {
		t.Errorf("state snapshot = %v, want the orchestrator's", states[1].Snapshot)
	}
}

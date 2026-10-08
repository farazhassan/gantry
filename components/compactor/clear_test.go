package compactor_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/compactor"
)

func TestClearToolResultsKeepsNewestNResults(t *testing.T) {
	c1 := gantry.Message{Role: gantry.RoleAssistant, ToolCalls: []gantry.ToolCall{{ID: "c1", Name: "t", Input: []byte(`{"path":"a"}`)}}}
	msgs := []gantry.Message{
		user("q1"), c1, result("c1", xs(100)),
		user("q2"), call("c2"), result("c2", xs(100)), assistant("done"),
	}
	got, err := compactor.ClearToolResults(1).Compact(context.Background(), msgs, lenBudget)
	if err != nil {
		t.Fatal(err)
	}
	if got[2].Content != clearedPlaceholder || got[2].ToolCallID != "c1" || got[2].Name != "t" {
		t.Errorf("older result = %+v, want cleared with ID and Name kept", got[2])
	}
	if got[5].Content != xs(100) {
		t.Errorf("newest result was cleared")
	}
	if string(got[1].ToolCalls[0].Input) != `{"path":"a"}` {
		t.Errorf("tool call Input = %q, want unchanged", got[1].ToolCalls[0].Input)
	}
	if msgs[2].Content != xs(100) {
		t.Errorf("input mutated")
	}
}

func TestClearToolResultsCountsResultsAcrossOneTurn(t *testing.T) {
	msgs := []gantry.Message{user("q")}
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("c%d", i)
		msgs = append(msgs, call(id), result(id, xs(100)))
	}
	got, _ := compactor.ClearToolResults(2).Compact(context.Background(), msgs, lenBudget)
	for i := 1; i <= 5; i++ {
		c := got[2*i].Content
		if cleared := c == clearedPlaceholder; cleared != (i <= 3) {
			t.Errorf("result c%d = %q, want cleared=%v", i, c, i <= 3)
		}
	}
}

func TestClearToolResultsStopsAtMaxTokens(t *testing.T) {
	msgs := []gantry.Message{
		user("q1"), call("c1"), result("c1", xs(100)), call("c2"), result("c2", xs(100)),
		user("q2"),
	}
	b := lenBudget
	b.MaxTokens = 150 // 204 → 141 after the first clear
	got, _ := compactor.ClearToolResults(0).Compact(context.Background(), msgs, b)
	if got[2].Content != clearedPlaceholder || got[4].Content != xs(100) {
		t.Errorf("got %q / %q, want only the oldest result cleared", got[2].Content, got[4].Content)
	}
}

func TestClearToolResultsSkipsShortResults(t *testing.T) {
	msgs := []gantry.Message{user("q1"), call("c1"), result("c1", "ok"), user("q2")}
	got, _ := compactor.ClearToolResults(0).Compact(context.Background(), msgs, lenBudget)
	if got[2].Content != "ok" {
		t.Errorf("short result = %q, want unchanged", got[2].Content)
	}
}

func TestClearToolResultsPanicsOnNegativeKeep(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic")
		}
	}()
	compactor.ClearToolResults(-1)
}

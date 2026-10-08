package compactor_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/compactor"
)

func TestClearToolResultsClearsOnlyOlderTurns(t *testing.T) {
	msgs := []gantry.Message{
		user("q1"), call("c1"), result("c1", xs(100)),
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
		t.Errorf("newest-turn result was cleared")
	}
	if msgs[2].Content != xs(100) {
		t.Errorf("input mutated")
	}
}

func TestClearToolResultsStopsAtMaxTokens(t *testing.T) {
	msgs := []gantry.Message{
		user("q1"), call("c1"), result("c1", xs(100)), call("c2"), result("c2", xs(100)),
		user("q2"),
	}
	b := lenBudget
	b.MaxTokens = 150 // 204 → 141 after the first clear
	got, _ := compactor.ClearToolResults(1).Compact(context.Background(), msgs, b)
	if got[2].Content != clearedPlaceholder || got[4].Content != xs(100) {
		t.Errorf("got %q / %q, want only the oldest result cleared", got[2].Content, got[4].Content)
	}
}

func TestClearToolResultsSkipsShortResults(t *testing.T) {
	msgs := []gantry.Message{user("q1"), call("c1"), result("c1", "ok"), user("q2")}
	got, _ := compactor.ClearToolResults(1).Compact(context.Background(), msgs, lenBudget)
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

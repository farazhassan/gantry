package compactor_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/compactor"
)

// fourTurns: preamble "rules" (5) + four turns of 4 tokens = 21.
func fourTurns() []gantry.Message {
	return []gantry.Message{
		{Role: gantry.RoleSystem, Content: "rules"},
		user("q1"), assistant("r1"),
		user("q2"), assistant("r2"),
		user("q3"), assistant("r3"),
		user("q4"), assistant("r4"),
	}
}

func contents(msgs []gantry.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Content
	}
	return out
}

func equalContents(t *testing.T, got []gantry.Message, want ...string) {
	t.Helper()
	g := contents(got)
	if len(g) != len(want) {
		t.Fatalf("got %q, want %q", g, want)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Fatalf("got %q, want %q", g, want)
		}
	}
}

func TestDropTurnsDropsAllOlderWithoutTarget(t *testing.T) {
	got, err := compactor.DropTurns(1, false).Compact(context.Background(), fourTurns(), lenBudget)
	if err != nil {
		t.Fatal(err)
	}
	equalContents(t, got, "rules", "q4", "r4")
}

func TestDropTurnsStopsAtMaxTokens(t *testing.T) {
	b := lenBudget
	b.MaxTokens = 13
	got, _ := compactor.DropTurns(1, false).Compact(context.Background(), fourTurns(), b)
	equalContents(t, got, "rules", "q3", "r3", "q4", "r4")
}

func TestDropTurnsPinFirst(t *testing.T) {
	got, _ := compactor.DropTurns(1, true).Compact(context.Background(), fourTurns(), lenBudget)
	equalContents(t, got, "rules", "q1", "r1", "q4", "r4")
}

func TestDropTurnsKeepsSummary(t *testing.T) {
	msgs := []gantry.Message{user(summaryPrefix + "s"), user("q1"), assistant("r1"), user("q2")}
	got, _ := compactor.DropTurns(1, false).Compact(context.Background(), msgs, lenBudget)
	equalContents(t, got, summaryPrefix+"s", "q2")
}

func TestDropTurnsNeverLeavesOrphanResult(t *testing.T) {
	msgs := []gantry.Message{user("q1"), call("c1"), result("c1", "r"), assistant("a"), user("q2")}
	got, _ := compactor.DropTurns(1, false).Compact(context.Background(), msgs, lenBudget)
	for _, m := range got {
		if m.Role == gantry.RoleTool {
			t.Errorf("orphaned tool result left: %+v", got)
		}
	}
}

func TestDropTurnsPanicsOnNegativeKeep(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic")
		}
	}()
	compactor.DropTurns(-1, false)
}

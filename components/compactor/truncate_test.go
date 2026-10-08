package compactor_test

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/compactor"
)

func TestTruncateMessagesCutsNewestToolResult(t *testing.T) {
	msgs := []gantry.Message{user("q"), call("c1"), result("c1", xs(1000))}
	got, err := compactor.TruncateMessages(100).Compact(context.Background(), msgs, lenBudget)
	if err != nil {
		t.Fatal(err)
	}
	c := got[2].Content
	if !strings.Contains(c, "tokens truncated") || len(c) >= 1000 || !strings.HasPrefix(c, xs(50)) || !strings.HasSuffix(c, xs(50)) {
		t.Errorf("truncated content = %q", c)
	}
	if msgs[2].Content != xs(1000) {
		t.Error("input mutated")
	}
}

func TestTruncateMessagesKeepsNewestUserInput(t *testing.T) {
	msgs := []gantry.Message{user(xs(1000)), assistant("r"), user(strings.Repeat("y", 1000))}
	got, _ := compactor.TruncateMessages(100).Compact(context.Background(), msgs, lenBudget)
	if !strings.Contains(got[0].Content, "tokens truncated") {
		t.Errorf("older user message not truncated")
	}
	if got[2].Content != strings.Repeat("y", 1000) {
		t.Errorf("newest user input was truncated")
	}
}

func TestTruncateMessagesIsUTF8Safe(t *testing.T) {
	msgs := []gantry.Message{user("q"), call("c1"), result("c1", strings.Repeat("€", 400))} // 1200 bytes
	got, _ := compactor.TruncateMessages(100).Compact(context.Background(), msgs, lenBudget)
	if !utf8.ValidString(got[2].Content) || !strings.Contains(got[2].Content, "tokens truncated") {
		t.Errorf("content = %q", got[2].Content)
	}
}

func TestTruncateMessagesSkipsSummaries(t *testing.T) {
	s := summaryPrefix + xs(1000)
	msgs := []gantry.Message{user(s), user("q")}
	got, _ := compactor.TruncateMessages(100).Compact(context.Background(), msgs, lenBudget)
	if got[0].Content != s {
		t.Error("summary was truncated")
	}
}

func TestTruncateMessagesPanicsOnNonPositiveMax(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic")
		}
	}()
	compactor.TruncateMessages(0)
}

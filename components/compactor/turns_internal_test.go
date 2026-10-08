package compactor

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/farazhassan/gantry"
)

func TestSegmentSplitsPreambleAndTurns(t *testing.T) {
	msgs := []gantry.Message{
		{Role: gantry.RoleSystem, Content: "rules"}, // 0 preamble
		{Role: gantry.RoleUser, Content: "q1"},      // 1 turn 0
		{Role: gantry.RoleAssistant, ToolCalls: []gantry.ToolCall{{ID: "c1"}}},
		{Role: gantry.RoleTool, ToolCallID: "c1", Content: "r"},                                         // 3
		gantry.WithTag(gantry.Message{Role: gantry.RoleUser, Content: summaryPrefix + "s"}, summaryTag), // 4 turn 1 (summary)
		{Role: gantry.RoleUser, Content: "q2"},                                                          // 5 turn 2
		{Role: gantry.RoleAssistant, Content: "a2"},                                                     // 6
	}
	pre, turns := segment(msgs)
	if pre != (span{0, 1}) {
		t.Errorf("preamble = %+v, want {0 1}", pre)
	}
	want := []span{{1, 4}, {4, 5}, {5, 7}}
	if len(turns) != len(want) {
		t.Fatalf("turns = %+v, want %+v", turns, want)
	}
	for i := range want {
		if turns[i] != want[i] {
			t.Errorf("turns[%d] = %+v, want %+v", i, turns[i], want[i])
		}
	}
	if !isSummary(msgs[4]) || isSummary(msgs[5]) {
		t.Error("isSummary misclassified")
	}
}

func TestSegmentNoUserMessages(t *testing.T) {
	msgs := []gantry.Message{{Content: "a"}, {Content: "b"}}
	pre, turns := segment(msgs)
	if pre != (span{0, 2}) || len(turns) != 0 {
		t.Errorf("pre=%+v turns=%+v, want all preamble", pre, turns)
	}
}

func TestOlderTurns(t *testing.T) {
	turns := []span{{0, 1}, {1, 2}, {2, 3}}
	if got := olderTurns(turns, 1); len(got) != 2 {
		t.Errorf("olderTurns(keep 1) = %v, want 2 turns", got)
	}
	if got := olderTurns(turns, 3); got != nil {
		t.Errorf("olderTurns(keep 3) = %v, want nil", got)
	}
}

func TestCapBytesIsUTF8Safe(t *testing.T) {
	s := strings.Repeat("€", 10) // 30 bytes
	got := capBytes(s, 8)
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "…") || len(got) > 8+len("…") {
		t.Errorf("capBytes = %q", got)
	}
	if capBytes("short", 10) != "short" {
		t.Error("capBytes changed a short string")
	}
}

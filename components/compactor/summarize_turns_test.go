package compactor_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/compactor"
	"github.com/farazhassan/gantry/eval"
)

func reply(s string) gantry.LLMResponse {
	return gantry.LLMResponse{Content: s, StopReason: gantry.StopReasonEnd}
}

func TestSummarizeTurnsReplacesOlderTurns(t *testing.T) {
	mock := eval.NewMockLLMClient(reply("S"))
	msgs := []gantry.Message{
		{Role: gantry.RoleSystem, Content: "rules"},
		user("q1" + xs(40)), assistant("r1" + xs(40)), user("q2" + xs(40)), assistant("r2" + xs(40)), user("q3"), assistant("r3"),
	}
	got, err := compactor.SummarizeTurns(mock, 1).Compact(context.Background(), msgs, lenBudget)
	if err != nil {
		t.Fatal(err)
	}
	equalContents(t, got, "rules", summaryPrefix+"S", "q3", "r3")
	if got[1].Role != gantry.RoleUser {
		t.Errorf("summary role = %q, want user", got[1].Role)
	}
	req := mock.Requests()[0]
	if req.MaxTokens != 1024 {
		t.Errorf("request MaxTokens = %d, want 1024", req.MaxTokens)
	}
	p := req.Messages[0].Content
	if !strings.Contains(p, "q1") || !strings.Contains(p, "r2") || strings.Contains(p, "q3") {
		t.Errorf("prompt = %q, want q1..r2 only", p)
	}
}

func TestSummarizeTurnsSelectsFewestTurns(t *testing.T) {
	mock := eval.NewMockLLMClient(reply("S"))
	msgs := []gantry.Message{user(xs(100)), assistant(xs(100)), user(xs(100)), assistant(xs(100)), user("q3"), assistant("r3")}
	b := lenBudget
	b.MaxTokens = 260 // 404 − 200 + (10 + 34 prefix) = 248 after one turn
	got, _ := compactor.SummarizeTurns(mock, 1, compactor.WithSummaryMaxTokens(10)).Compact(context.Background(), msgs, b)
	if len(got) != 5 || got[0].Content != summaryPrefix+"S" {
		t.Errorf("got %q, want summary + 4 kept messages", contents(got))
	}
	if mock.Requests()[0].MaxTokens != 10 {
		t.Errorf("request MaxTokens = %d, want 10", mock.Requests()[0].MaxTokens)
	}
}

func TestSummarizeTurnsRollsPriorSummary(t *testing.T) {
	mock := eval.NewMockLLMClient(reply("NEW"))
	msgs := []gantry.Message{summary("OLD"), user("q1"), assistant("r1"), user("q2"), assistant("r2")}
	got, _ := compactor.SummarizeTurns(mock, 1).Compact(context.Background(), msgs, lenBudget)
	equalContents(t, got, summaryPrefix+"NEW", "q2", "r2")
	if !strings.Contains(mock.Requests()[0].Messages[0].Content, "OLD") {
		t.Error("prior summary not passed to the summarizer")
	}
}

func TestSummarizeTurnsSkipsLLMWhenAlreadyFits(t *testing.T) {
	mock := eval.NewMockLLMClient(reply("S"))
	msgs := []gantry.Message{user("q1"), assistant("r1"), user("q2"), assistant("r2"), user("q3")}
	b := lenBudget
	b.MaxTokens = 100000
	got, _ := compactor.SummarizeTurns(mock, 1).Compact(context.Background(), msgs, b)
	equalContents(t, got, "q1", "r1", "q2", "r2", "q3")
	if n := len(mock.Requests()); n != 0 {
		t.Errorf("LLM calls = %d, want 0", n)
	}
}

func TestSummarizeTurnsRollsPriorSummaryAfterOtherTurns(t *testing.T) {
	mock := eval.NewMockLLMClient(reply("NEW"))
	msgs := []gantry.Message{user(xs(100)), summary("OLD"), user("q1"), assistant("a"), user("q2")}
	b := lenBudget
	b.MaxTokens = 60 // the first turn alone would fit: 143 − 100 + 10
	got, _ := compactor.SummarizeTurns(mock, 1, compactor.WithSummaryMaxTokens(10)).Compact(context.Background(), msgs, b)
	summaries := 0
	for _, m := range got {
		if strings.HasPrefix(m.Content, summaryPrefix) {
			summaries++
		}
	}
	if summaries != 1 {
		t.Errorf("got %q, want exactly one summary", contents(got))
	}
	if !strings.Contains(mock.Requests()[0].Messages[0].Content, "OLD") {
		t.Error("prior summary not passed to the summarizer")
	}
}

func TestSummarizeTurnsOnlySummaryCandidateSkipsLLM(t *testing.T) {
	mock := eval.NewMockLLMClient(reply("NEW"))
	msgs := []gantry.Message{summary("OLD"), user("q2"), assistant("r2")}
	got, _ := compactor.SummarizeTurns(mock, 1).Compact(context.Background(), msgs, lenBudget)
	equalContents(t, got, summaryPrefix+"OLD", "q2", "r2")
	if n := len(mock.Requests()); n != 0 {
		t.Errorf("LLM calls = %d, want 0", n)
	}
}

func TestSummarizeTurnsCapsEachMessageInPrompt(t *testing.T) {
	mock := eval.NewMockLLMClient(reply("S"))
	msgs := []gantry.Message{user(xs(5000)), assistant("r1"), user("q2")}
	_, _ = compactor.SummarizeTurns(mock, 1).Compact(context.Background(), msgs, lenBudget)
	p := mock.Requests()[0].Messages[0].Content
	// The rendered line "user: " + content is capped at 2,000 bytes.
	if !strings.Contains(p, "user: "+xs(2000-len("user: "))+"…") || strings.Contains(p, xs(2000-len("user: ")+1)) {
		t.Errorf("prompt not capped at 2000 bytes per message (len %d)", len(p))
	}
}

func TestSummarizeTurnsReturnsLLMError(t *testing.T) {
	boom := errors.New("boom")
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{{Err: boom}})
	msgs := []gantry.Message{user("q1"), assistant("r1"), user("q2")}
	if _, err := compactor.SummarizeTurns(mock, 1).Compact(context.Background(), msgs, lenBudget); !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
}

func TestSummarizeTurnsEmptyResponseLeavesInput(t *testing.T) {
	mock := eval.NewMockLLMClient(reply("  "))
	msgs := []gantry.Message{user("q1"), assistant("r1"), user("q2")}
	got, err := compactor.SummarizeTurns(mock, 1).Compact(context.Background(), msgs, lenBudget)
	if err != nil {
		t.Fatal(err)
	}
	equalContents(t, got, "q1", "r1", "q2")
}

func TestSummarizeTurnsNothingOlderSkipsLLM(t *testing.T) {
	mock := eval.NewMockLLMClient()
	msgs := []gantry.Message{user("q1"), assistant("r1")}
	got, _ := compactor.SummarizeTurns(mock, 1).Compact(context.Background(), msgs, lenBudget)
	if len(got) != 2 || len(mock.Requests()) != 0 {
		t.Errorf("got %d msgs, %d LLM calls; want 2, 0", len(got), len(mock.Requests()))
	}
}

func TestSummarizeTurnsValidatesArgs(t *testing.T) {
	for name, f := range map[string]func(){
		"nil client":    func() { compactor.SummarizeTurns(nil, 1) },
		"negative keep": func() { compactor.SummarizeTurns(eval.NewMockLLMClient(), -1) },
		"zero max":      func() { compactor.WithSummaryMaxTokens(0) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("expected panic")
				}
			}()
			f()
		})
	}
}

func TestSummarizeTurnsMarksSummaryAndCapsPriorOne(t *testing.T) {
	mock := eval.NewMockLLMClient(reply("NEW"))
	msgs := []gantry.Message{summary(xs(5000)), user("q1"), assistant("r1"), user("q2")}
	got, _ := compactor.SummarizeTurns(mock, 1).Compact(context.Background(), msgs, lenBudget)
	if gantry.Tag(got[0]) != "components/compactor:summary" || got[0].Content != summaryPrefix+"NEW" {
		t.Errorf("summary = %+v, want marked summary", got[0])
	}
	p := mock.Requests()[0].Messages[0].Content
	if strings.Contains(p, xs(2001)) {
		t.Errorf("prior summary not capped in prompt (len %d)", len(p))
	}
}

func TestSummarizeTurnsReservesSummaryPrefix(t *testing.T) {
	mock := eval.NewMockLLMClient(reply("S"))
	msgs := []gantry.Message{user(xs(100)), assistant(xs(100)), user(xs(100)), assistant(xs(100)), user("q3"), assistant("r3")}
	b := lenBudget
	b.MaxTokens = 220 // one turn would leave 404 − 200 + 10 + 34 = 248
	got, _ := compactor.SummarizeTurns(mock, 1, compactor.WithSummaryMaxTokens(10)).Compact(context.Background(), msgs, b)
	equalContents(t, got, summaryPrefix+"S", "q3", "r3")
}

func TestSummarizeTurnsCapsWholeRenderedMessage(t *testing.T) {
	mock := eval.NewMockLLMClient(reply("S"))
	many := gantry.Message{Role: gantry.RoleAssistant}
	for i := range 50 {
		many.ToolCalls = append(many.ToolCalls, gantry.ToolCall{ID: fmt.Sprint(i), Name: "t", Input: []byte(`"` + xs(300) + `"`)})
	}
	msgs := []gantry.Message{user("q1"), many, user("q2")}
	_, _ = compactor.SummarizeTurns(mock, 1).Compact(context.Background(), msgs, lenBudget)
	p := mock.Requests()[0].Messages[0].Content
	if len(p) > 2*2000+500 { // two messages, each capped at 2,000 bytes, plus the instruction
		t.Errorf("summarizer prompt is %d bytes; tool-call previews are not capped per message", len(p))
	}
}

func TestSummarizeTurnsBoundsSummaryByRemainingBudget(t *testing.T) {
	mock := eval.NewMockLLMClient(reply("S"))
	msgs := []gantry.Message{user(xs(100)), assistant(xs(100)), user(xs(100)), assistant(xs(100)), user("q3"), assistant("r3")}
	b := lenBudget
	b.MaxTokens = 150 // after both older turns: 150 − 4 kept − 34 prefix = 112 left
	_, _ = compactor.SummarizeTurns(mock, 1).Compact(context.Background(), msgs, b)
	if got := mock.Requests()[0].MaxTokens; got != 112 {
		t.Errorf("summary MaxTokens = %d, want 112 (remaining budget)", got)
	}
}

func TestSummarizeTurnsRejectsSummaryThatDoesNotShrink(t *testing.T) {
	mock := eval.NewMockLLMClient(reply(xs(1000)))
	msgs := []gantry.Message{user("q1"), assistant("r1"), user("q2"), assistant("r2"), user("q3")}
	got, err := compactor.SummarizeTurns(mock, 1).Compact(context.Background(), msgs, lenBudget)
	if err != nil {
		t.Fatal(err)
	}
	equalContents(t, got, "q1", "r1", "q2", "r2", "q3")
}

func TestSummarizeTurnsSkipsWhenNoRoomForSummary(t *testing.T) {
	mock := eval.NewMockLLMClient(reply("S"))
	msgs := []gantry.Message{user(xs(100)), assistant(xs(100)), user("q3"), assistant("r3")}
	b := lenBudget
	b.MaxTokens = 30 // kept 4 + 34-token prefix already exceed it
	got, err := compactor.SummarizeTurns(mock, 1).Compact(context.Background(), msgs, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || len(mock.Requests()) != 0 {
		t.Errorf("got %d msgs and %d LLM calls; want input unchanged and no call", len(got), len(mock.Requests()))
	}
}

func TestSummarizeTurnsCapsWholePrompt(t *testing.T) {
	mock := eval.NewMockLLMClient(reply("S"))
	var msgs []gantry.Message
	for range 100 {
		msgs = append(msgs, user(xs(1000)), assistant(xs(1000)))
	}
	msgs = append(msgs, user("q"))
	got, _ := compactor.SummarizeTurns(mock, 1).Compact(context.Background(), msgs, lenBudget)
	p := mock.Requests()[0].Messages[0].Content
	if len(p) > 32_000 || strings.Contains(p, "omitted") {
		t.Errorf("summarizer prompt is %d bytes (omitted: %v); want ≤ 32,000 with whole turns only", len(p), strings.Contains(p, "omitted"))
	}
	// Only the turns that fit in the prompt are replaced; the rest are kept.
	if len(got) < 100 {
		t.Errorf("got %d messages; turns left out of the prompt must not be replaced", len(got))
	}
}

func TestSummarizeTurnsLeavesTurnTooLargeForPrompt(t *testing.T) {
	mock := eval.NewMockLLMClient(reply("S"))
	msgs := []gantry.Message{user("q1")}
	for i := range 30 { // one turn rendering to ~45 KB, over the prompt cap
		id := fmt.Sprint(i)
		msgs = append(msgs, call(id), result(id, xs(1500)))
	}
	msgs = append(msgs, user("q2"))
	got, err := compactor.SummarizeTurns(mock, 1).Compact(context.Background(), msgs, lenBudget)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(msgs) || len(mock.Requests()) != 0 {
		t.Errorf("got %d msgs and %d LLM calls; want input unchanged and no call", len(got), len(mock.Requests()))
	}
}

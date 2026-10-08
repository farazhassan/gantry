package compactor_test

import (
	"context"
	"errors"
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
		user("q1"), assistant("r1"), user("q2"), assistant("r2"), user("q3"), assistant("r3"),
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
	if strings.Contains(p, xs(2001)) || !strings.Contains(p, xs(2000)) {
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

package compactor_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/compactor"
	"github.com/farazhassan/gantry/eval"
)

// seed replaces the transcript once, on the first context assembly.
func seed(t *testing.T, a *gantry.Agent, msgs []gantry.Message) {
	t.Helper()
	done := false
	if err := a.UseNamed(gantry.PhaseAssembleContext, "seed", func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if !done {
				done = true
				s.Messages = append([]gantry.Message(nil), msgs...)
			}
			return next(ctx, s)
		}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestPolicyCompactsProactivelyAtTrigger(t *testing.T) {
	mock := eval.NewMockLLMClient(reply("ok"))
	a, _ := gantry.NewAgent(gantry.WithLLM(mock), gantry.WithContextWindow(1000))
	seed(t, a, []gantry.Message{user("q1"), call("c1"), result("c1", xs(900)), assistant("done1"), user("q2")}) // 909 ≥ 800
	p := compactor.NewPolicy(compactor.Policy{Steps: []compactor.Compactor{compactor.ClearToolResults(0)}})
	_ = a.With(compactor.New(p, compactor.Budget{Counter: lenCount}))

	s, err := a.Run(context.Background(), "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := mock.Requests()[0].Messages[2].Content; got != clearedPlaceholder {
		t.Errorf("sent tool result = %q, want cleared", got)
	}
	r, _ := s.Meta[compactor.MetaLastCompaction].(*compactor.Report)
	if r == nil || r.Forced || r.BeforeTokens != 909 || r.AfterTokens >= 500 || r.Steps[0].Name != "clear_tool_results" {
		t.Errorf("report = %+v", r)
	}
}

func TestPolicyForcedByProviderOverflow(t *testing.T) {
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Err: overflow(10)},
		{Response: reply("ok")},
	})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock)) // window unknown: no proactive compaction
	seed(t, a, []gantry.Message{user("aaaaa"), assistant("bbbbb"), user("ccccc"), assistant("ddddd"), user("eeeee"), assistant("fffff")})
	p := compactor.NewPolicy(compactor.Policy{Steps: []compactor.Compactor{compactor.DropTurns(1, false)}})
	_ = a.With(compactor.New(p, compactor.Budget{Counter: lenCount}))

	s, err := a.Run(context.Background(), "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	reqs := mock.Requests()
	if len(reqs) != 2 || len(reqs[0].Messages) != 6 || len(reqs[1].Messages) != 2 {
		t.Fatalf("requests = %d (first %d msgs), want 2 with 6 then 2 messages", len(reqs), len(reqs[0].Messages))
	}
	if r, _ := s.Meta[compactor.MetaLastCompaction].(*compactor.Report); r == nil || !r.Forced || r.TargetTokens != 10 {
		t.Errorf("report = %+v, want forced with target 10", r)
	}
}

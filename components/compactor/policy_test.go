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

// fakeStep records every Budget it is given and returns a copy (or err).
type fakeStep struct {
	err     error
	budgets []compactor.Budget
}

func (*fakeStep) Name() string { return "fake" }

func (f *fakeStep) Compact(_ context.Context, msgs []gantry.Message, b compactor.Budget) ([]gantry.Message, error) {
	f.budgets = append(f.budgets, b)
	if f.err != nil {
		return nil, f.err
	}
	out := make([]gantry.Message, len(msgs))
	copy(out, msgs)
	return out, nil
}

func policyBudget(window, prompt int) compactor.Budget {
	b := lenBudget
	b.ContextWindow, b.PromptTokens = window, prompt
	return b
}

func TestPolicyBelowTriggerIsUnchanged(t *testing.T) {
	f := &fakeStep{}
	got, err := compactor.NewPolicy(compactor.Policy{Steps: []compactor.Compactor{f}}).
		Compact(context.Background(), userTurns(7, 100), policyBudget(1000, 700))
	if err != nil || len(got) != 7 || len(f.budgets) != 0 {
		t.Errorf("got %d msgs, err %v, %d step calls; want 7, nil, 0", len(got), err, len(f.budgets))
	}
}

func TestPolicyCompactsToTarget(t *testing.T) {
	p := compactor.NewPolicy(compactor.Policy{Steps: []compactor.Compactor{compactor.DropTurns(0, false)}})
	got, _ := p.Compact(context.Background(), userTurns(10, 100), policyBudget(1000, 1000))
	if len(got) != 5 {
		t.Errorf("len = %d, want 5 (target 500 of 1000)", len(got))
	}
}

func TestPolicyUsesTokenFallbackWithoutWindow(t *testing.T) {
	p := compactor.NewPolicy(compactor.Policy{TriggerTokens: 800, TargetTokens: 500,
		Steps: []compactor.Compactor{compactor.DropTurns(0, false)}})
	got, _ := p.Compact(context.Background(), userTurns(10, 100), policyBudget(0, 1000))
	if len(got) != 5 {
		t.Errorf("len = %d, want 5", len(got))
	}
}

func TestPolicyWithoutLimitsIsNoOp(t *testing.T) {
	f := &fakeStep{}
	got, _ := compactor.NewPolicy(compactor.Policy{Steps: []compactor.Compactor{f}}).
		Compact(context.Background(), userTurns(10, 100), policyBudget(0, 5000))
	if len(got) != 10 || len(f.budgets) != 0 {
		t.Errorf("got %d msgs, %d step calls; want 10, 0", len(got), len(f.budgets))
	}
}

func TestPolicyForceIgnoresTriggerAndTakesMin(t *testing.T) {
	f := &fakeStep{}
	b := policyBudget(1000, 100) // far below the trigger
	b.Force, b.MaxTokens = true, 300
	_, _ = compactor.NewPolicy(compactor.Policy{Steps: []compactor.Compactor{f}}).
		Compact(context.Background(), userTurns(10, 100), b)
	if len(f.budgets) != 1 || f.budgets[0].MaxTokens != 300 || !f.budgets[0].Force {
		t.Errorf("step budgets = %+v, want one forced call with MaxTokens 300", f.budgets)
	}
}

func TestPolicySubtractsFixedTokens(t *testing.T) {
	f := &fakeStep{}
	b := policyBudget(1000, 1000)
	b.FixedTokens = 200
	_, _ = compactor.NewPolicy(compactor.Policy{Steps: []compactor.Compactor{f}}).
		Compact(context.Background(), userTurns(8, 100), b)
	if len(f.budgets) != 1 || f.budgets[0].MaxTokens != 300 {
		t.Errorf("step MaxTokens = %+v, want 300 (500 − 200 fixed)", f.budgets)
	}
}

func TestPolicyStopsAtFirstStepMeetingTarget(t *testing.T) {
	f := &fakeStep{}
	p := compactor.NewPolicy(compactor.Policy{Steps: []compactor.Compactor{compactor.DropTurns(0, false), f}})
	_, _ = p.Compact(context.Background(), userTurns(10, 100), policyBudget(1000, 1000))
	if len(f.budgets) != 0 {
		t.Errorf("second step ran %d times, want 0", len(f.budgets))
	}
}

func TestPolicyWrapsStepError(t *testing.T) {
	boom := errors.New("boom")
	p := compactor.NewPolicy(compactor.Policy{Steps: []compactor.Compactor{&fakeStep{err: boom}}})
	_, err := p.Compact(context.Background(), userTurns(10, 100), policyBudget(1000, 1000))
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "policy step 0 (fake)") {
		t.Errorf("err = %v", err)
	}
}

func TestPolicyRepairsOrphanedToolResults(t *testing.T) {
	msgs := []gantry.Message{user("q1"), call("c1"), result("c1", xs(100)), assistant("done"), user("q2")}
	p := compactor.NewPolicy(compactor.Policy{Steps: []compactor.Compactor{compactor.NewSlidingWindow(3)}})
	got, _ := p.Compact(context.Background(), msgs, policyBudget(100, 108))
	for _, m := range got {
		if m.Role == gantry.RoleTool {
			t.Errorf("orphaned tool result kept: %q", contents(got))
		}
	}
	equalContents(t, got, "done", "q2")
}

func TestPolicyCalibratesCounter(t *testing.T) {
	probe := gantry.Message{Content: "abcd"}
	for _, tc := range []struct {
		name   string
		prompt int
		want   int
	}{{"2x", 2000, 8}, {"clamped", 20000, 8}} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeStep{}
			b := policyBudget(tc.prompt, tc.prompt)
			b.Force, b.MaxTokens = true, 10
			_, _ = compactor.NewPolicy(compactor.Policy{Steps: []compactor.Compactor{f}}).
				Compact(context.Background(), userTurns(10, 100), b)
			if got := f.budgets[0].Count(probe); got != tc.want {
				t.Errorf("calibrated Count = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestNewPolicyValidates(t *testing.T) {
	step := []compactor.Compactor{compactor.DropTurns(0, false)}
	for name, p := range map[string]compactor.Policy{
		"no steps":              {},
		"nil step":              {Steps: []compactor.Compactor{nil}},
		"target above trigger":  {Trigger: 0.5, Target: 0.6, Steps: step},
		"trigger above 1":       {Trigger: 1.5, Steps: step},
		"tokens inverted":       {TriggerTokens: 100, TargetTokens: 200, Steps: step},
		"only trigger tokens":   {TriggerTokens: 100, Steps: step},
		"negative target ratio": {Target: -0.1, Steps: step},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("expected panic")
				}
			}()
			compactor.NewPolicy(p)
		})
	}
}

func TestMiddlewareStoresReportOnlyWhenTriggered(t *testing.T) {
	for _, tc := range []struct {
		name   string
		window int
		want   bool
	}{{"below trigger", 100000, false}, {"triggered", 100, true}} {
		t.Run(tc.name, func(t *testing.T) {
			mock := eval.NewMockLLMClient(reply("ok"))
			a, _ := gantry.NewAgent(gantry.WithLLM(mock), gantry.WithContextWindow(tc.window))
			preload(t, a, 30) // 30 × "msg" ≈ 150 tokens with the default counter
			p := compactor.NewPolicy(compactor.Policy{Steps: []compactor.Compactor{compactor.DropTurns(1, false)}})
			_ = a.With(compactor.New(p, compactor.Budget{}))
			s, err := a.Run(context.Background(), "")
			if err != nil {
				t.Fatal(err)
			}
			r, ok := s.Meta[compactor.MetaLastCompaction].(*compactor.Report)
			if ok != tc.want {
				t.Fatalf("report present = %v, want %v", ok, tc.want)
			}
			if ok && (r.AfterTokens >= r.BeforeTokens || len(r.Steps) != 1 || r.Steps[0].Name != "drop_turns") {
				t.Errorf("report = %+v", r)
			}
		})
	}
}

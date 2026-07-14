package router_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/router"
	"github.com/farazhassan/gantry/eval"
)

// dispatchFixture wires two real agents behind a rule classifier:
// inputs starting "calc:" go to math, everything else to poetry.
type dispatchFixture struct {
	router    *router.Router
	mathLLM   *eval.MockLLMClient
	poetryLLM *eval.MockLLMClient
}

func newDispatchFixture(t *testing.T) *dispatchFixture {
	t.Helper()
	mathLLM := eval.NewMockLLMClient(gantry.LLMResponse{Content: "math says 4"})
	poetryLLM := eval.NewMockLLMClient(gantry.LLMResponse{Content: "poetry says roses"})
	mathAgent, err := gantry.NewAgent(gantry.WithLLM(mathLLM))
	if err != nil {
		t.Fatalf("NewAgent math: %v", err)
	}
	poetryAgent, err := gantry.NewAgent(gantry.WithLLM(poetryLLM))
	if err != nil {
		t.Fatalf("NewAgent poetry: %v", err)
	}
	reg := router.NewRegistry()
	if err := reg.Add("math", "Arithmetic.", mathAgent); err != nil {
		t.Fatalf("Add math: %v", err)
	}
	if err := reg.Add("poetry", "Poems.", poetryAgent); err != nil {
		t.Fatalf("Add poetry: %v", err)
	}
	rules := router.NewRuleRouter(
		prefixRule("calc:", "math"),
		func(_ context.Context, _ string) (string, bool) { return "poetry", true },
	)
	r, err := router.New(rules, reg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &dispatchFixture{router: r, mathLLM: mathLLM, poetryLLM: poetryLLM}
}

func TestRouterRunDispatchesToSelectedAgent(t *testing.T) {
	f := newDispatchFixture(t)
	key, st, err := f.router.Run(context.Background(), "calc: 2+2")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if key != "math" {
		t.Errorf("key = %q, want math", key)
	}
	if st == nil || !st.Done || st.DoneReason != gantry.DoneNoToolCalls {
		t.Fatalf("state = %+v, want terminal DoneNoToolCalls", st)
	}
	if st.FinalOutput != "math says 4" {
		t.Errorf("FinalOutput = %q, want %q", st.FinalOutput, "math says 4")
	}
	if n := len(f.poetryLLM.Requests()); n != 0 {
		t.Errorf("poetry LLM consulted %d times, want 0", n)
	}
}

func TestRouterRunClassifierErrorReturnsNilState(t *testing.T) {
	reg := router.NewRegistry()
	if err := reg.Add("math", "Arithmetic.", newTestAgent(t)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	r, err := router.New(router.NewRuleRouter(), reg) // no rules: always ErrNoRoute
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	key, st, err := r.Run(context.Background(), "anything")
	if !errors.Is(err, router.ErrNoRoute) {
		t.Errorf("err = %v, want ErrNoRoute", err)
	}
	if key != "" || st != nil {
		t.Errorf("(key, state) = (%q, %v), want empty key and nil state", key, st)
	}
}

func TestRouterRunUnregisteredKeyIsError(t *testing.T) {
	reg := router.NewRegistry()
	if err := reg.Add("math", "Arithmetic.", newTestAgent(t)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	ghost := classifierFunc(func(context.Context, string, []gantry.Message) (string, error) {
		return "ghost", nil
	})
	r, err := router.New(ghost, reg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _, err = r.Run(context.Background(), "anything")
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("err = %v, want an error naming the unregistered key", err)
	}
}

func TestRouterRunFromContinuesTranscriptAcrossAgents(t *testing.T) {
	f := newDispatchFixture(t)
	ctx := context.Background()

	_, st1, err := f.router.Run(ctx, "calc: 2+2")
	if err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	key2, st2, err := f.router.RunFrom(ctx, st1, "now write me a poem")
	if err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	if key2 != "poetry" {
		t.Errorf("turn-2 key = %q, want poetry", key2)
	}
	if st2.FinalOutput != "poetry says roses" {
		t.Errorf("FinalOutput = %q, want poetry says roses", st2.FinalOutput)
	}
	// The poetry agent saw the math turn: prior transcript + the new input.
	reqs := f.poetryLLM.Requests()
	if len(reqs) != 1 {
		t.Fatalf("poetry LLM requests = %d, want 1", len(reqs))
	}
	got := reqs[0].Messages
	want := []gantry.Message{
		{Role: gantry.RoleUser, Content: "calc: 2+2"},
		{Role: gantry.RoleAssistant, Content: "math says 4"},
		{Role: gantry.RoleUser, Content: "now write me a poem"},
	}
	if len(got) != len(want) {
		t.Fatalf("messages = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content {
			t.Errorf("message[%d] = (%s, %q), want (%s, %q)",
				i, got[i].Role, got[i].Content, want[i].Role, want[i].Content)
		}
	}
}

func TestRouterRunFromNilPriorBehavesLikeRun(t *testing.T) {
	f := newDispatchFixture(t)
	key, st, err := f.router.RunFrom(context.Background(), nil, "calc: 2+2")
	if err != nil {
		t.Fatalf("RunFrom(nil): %v", err)
	}
	if key != "math" || st.FinalOutput != "math says 4" {
		t.Errorf("(key, FinalOutput) = (%q, %q), want (math, math says 4)", key, st.FinalOutput)
	}
}

func TestRouterRunFromPassesRecentToClassifier(t *testing.T) {
	llm := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok"})
	agent, err := gantry.NewAgent(gantry.WithLLM(llm))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	reg := router.NewRegistry()
	if err := reg.Add("math", "Arithmetic.", agent); err != nil {
		t.Fatalf("Add: %v", err)
	}
	var gotRecent []gantry.Message
	rec := classifierFunc(func(_ context.Context, _ string, recent []gantry.Message) (string, error) {
		gotRecent = recent
		return "math", nil
	})
	r, err := router.New(rec, reg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	prior := &gantry.State{Messages: []gantry.Message{
		{Role: gantry.RoleUser, Content: "earlier question"},
		{Role: gantry.RoleAssistant, Content: "earlier answer"},
	}}
	if _, _, err := r.RunFrom(context.Background(), prior, "follow-up"); err != nil {
		t.Fatalf("RunFrom: %v", err)
	}
	if len(gotRecent) != 2 || gotRecent[0].Content != "earlier question" {
		t.Errorf("classifier recent = %+v, want the prior transcript", gotRecent)
	}
}

func TestNewRouterValidation(t *testing.T) {
	reg := router.NewRegistry()
	if _, err := router.New(nil, reg); err == nil {
		t.Errorf("New(nil classifier) = nil error, want an error")
	}
	if _, err := router.New(router.NewRuleRouter(), nil); err == nil {
		t.Errorf("New(nil registry) = nil error, want an error")
	}
	if _, err := router.New(router.NewRuleRouter(), reg); err == nil {
		t.Errorf("New(empty registry) = nil error, want an error")
	}
}

package gantry

import (
	"context"
	"reflect"
	"testing"
)

func TestToolRefs(t *testing.T) {
	defs := []ToolDef{{Name: "search"}, {Name: "calc"}}
	got := toolRefs(defs)
	want := []string{"search", "calc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("toolRefs = %v, want %v", got, want)
	}
	if toolRefs(nil) != nil {
		t.Fatalf("toolRefs(nil) = %v, want nil", toolRefs(nil))
	}
}

func TestCloneMessagesIsIndependent(t *testing.T) {
	src := []Message{{Role: RoleUser, Content: "hi"}}
	cp := cloneMessages(src)
	if !reflect.DeepEqual(cp, src) {
		t.Fatalf("clone = %v, want %v", cp, src)
	}
	cp[0].Content = "mutated"
	if src[0].Content != "hi" {
		t.Fatal("cloneMessages must not alias the source backing array")
	}
	if cloneMessages(nil) != nil {
		t.Fatal("cloneMessages(nil) must be nil")
	}
}

// stubLLM returns a fixed response with no tool calls so the run finishes in
// one iteration.
type stubLLM struct{ resp LLMResponse }

func (s stubLLM) Generate(ctx context.Context, req LLMRequest) (LLMResponse, error) {
	return s.resp, nil
}

// findSpanEnd returns the KindSpanEnd event for the named span.
func findSpanEnd(t *testing.T, tr *Trace, name string) TraceEvent {
	t.Helper()
	for _, ev := range tr.Snapshot() {
		if ev.Name == name && ev.Kind == KindSpanEnd {
			return ev
		}
	}
	t.Fatalf("no span-end event named %q", name)
	return TraceEvent{}
}

func TestRunSpanCapturesInputOutputState(t *testing.T) {
	llm := stubLLM{resp: LLMResponse{Content: "the answer"}}
	a, err := NewAgent(WithLLM(llm))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	state, err := a.Run(context.Background(), "the question")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	run := findSpanEnd(t, state.Trace, "run")
	if run.Attrs[AttrInput] != "the question" {
		t.Fatalf("run input = %v, want 'the question'", run.Attrs[AttrInput])
	}
	if run.Attrs[AttrOutput] != "the answer" {
		t.Fatalf("run output = %v, want 'the answer'", run.Attrs[AttrOutput])
	}
	if _, ok := run.Attrs[AttrState].(stateView); !ok {
		t.Fatalf("run state attr = %T, want stateView", run.Attrs[AttrState])
	}
}

func TestLLMCallSpanCapturesGeneration(t *testing.T) {
	llm := stubLLM{resp: LLMResponse{
		Content: "the answer",
		Usage:   Usage{InputTokens: 7, OutputTokens: 5},
	}}
	a, err := NewAgent(WithLLM(llm))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	state, err := a.Run(context.Background(), "the question")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	gen := findSpanEnd(t, state.Trace, "phase:llm_call")
	if gen.Attrs[AttrObservationType] != ObservationGeneration {
		t.Fatalf("observation type = %v, want %q", gen.Attrs[AttrObservationType], ObservationGeneration)
	}
	in, ok := gen.Attrs[AttrInput].(genInput)
	if !ok {
		t.Fatalf("llm_call input = %T, want genInput", gen.Attrs[AttrInput])
	}
	if len(in.Messages) == 0 || in.Messages[0].Content != "the question" {
		t.Fatalf("generation input messages = %v, want the seeded user message", in.Messages)
	}
	out, ok := gen.Attrs[AttrOutput].(genOutput)
	if !ok || out.Content != "the answer" {
		t.Fatalf("generation output = %v, want content 'the answer'", gen.Attrs[AttrOutput])
	}
	if u, ok := gen.Attrs[AttrUsage].(Usage); !ok || u.OutputTokens != 5 {
		t.Fatalf("generation usage = %v, want OutputTokens=5", gen.Attrs[AttrUsage])
	}
}

func TestStateSnapshotSanitizes(t *testing.T) {
	s := &State{
		Input:    "do the thing",
		System:   "sys",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
		Tools:    []ToolDef{{Name: "search", Description: "long text", Schema: []byte(`{"x":1}`)}},
		Trace:    NewTrace(),
		Usage:    Usage{InputTokens: 3, OutputTokens: 4},
	}
	v := stateSnapshot(s)

	if v.Input != "do the thing" || v.System != "sys" {
		t.Fatalf("scalar fields not copied: %+v", v)
	}
	if !reflect.DeepEqual(v.ToolRefs, []string{"search"}) {
		t.Fatalf("ToolRefs = %v, want [search]", v.ToolRefs)
	}
	// Mutating the snapshot's messages must not touch the source.
	v.Messages[0].Content = "mutated"
	if s.Messages[0].Content != "hi" {
		t.Fatal("stateSnapshot must clone Messages, not alias them")
	}
}

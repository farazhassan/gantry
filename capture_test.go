package gantry

import (
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

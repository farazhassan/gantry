package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
)

func TestRegistryAddAndLookup(t *testing.T) {
	r := tool.NewRegistry()
	r.Add(echoTool{})

	got, ok := r.Lookup("echo")
	if !ok {
		t.Fatalf("Lookup(echo) not found")
	}
	if got.Definition().Name != "echo" {
		t.Errorf("Definition().Name = %q", got.Definition().Name)
	}
}

func TestRegistryDefinitions(t *testing.T) {
	r := tool.NewRegistry()
	r.Add(echoTool{})
	defs := r.Definitions()
	if len(defs) != 1 || defs[0].Name != "echo" {
		t.Errorf("Definitions() = %+v", defs)
	}
}

func TestRegistryDuplicateOverrides(t *testing.T) {
	r := tool.NewRegistry()
	r.Add(echoTool{})
	r.Add(echoTool{}) // same name overrides
	if len(r.Definitions()) != 1 {
		t.Errorf("expected single tool after re-add")
	}
}

func TestRegistryInvokeUnknownReturnsError(t *testing.T) {
	r := tool.NewRegistry()
	_, err := r.Invoke(context.Background(), gantry.ToolCall{Name: "ghost", Input: json.RawMessage(`{}`)})
	if err == nil {
		t.Errorf("expected error invoking unknown tool")
	}
	if !errors.Is(err, gantry.ErrToolExecution) {
		t.Errorf("err should wrap ErrToolExecution; got %v", err)
	}
}

func TestRegistryInvokeSuccess(t *testing.T) {
	r := tool.NewRegistry()
	r.Add(echoTool{})
	out, err := r.Invoke(context.Background(), gantry.ToolCall{Name: "echo", Input: json.RawMessage(`{"a":1}`)})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if string(out) != `{"a":1}` {
		t.Errorf("Invoke returned %q", string(out))
	}
}

type authFailingTool struct{}

func (authFailingTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{Name: "auth_failing", Description: "always fails with ErrToolAuth", Schema: json.RawMessage(`{}`)}
}

func (authFailingTool) Invoke(context.Context, json.RawMessage) (json.RawMessage, error) {
	return nil, fmt.Errorf("%w: token expired", gantry.ErrToolAuth)
}

func TestRegistryInvokePreservesToolSentinel(t *testing.T) {
	r := tool.NewRegistry()
	r.Add(authFailingTool{})
	_, err := r.Invoke(context.Background(), gantry.ToolCall{Name: "auth_failing", Input: json.RawMessage(`{}`)})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, gantry.ErrToolExecution) {
		t.Errorf("err should still wrap ErrToolExecution; got %v", err)
	}
	if !errors.Is(err, gantry.ErrToolAuth) {
		t.Errorf("err should also wrap the tool's own ErrToolAuth; got %v", err)
	}
}

func TestRegistryInvokePassesPendingResultThroughUnwrapped(t *testing.T) {
	r := tool.NewRegistry()
	want := &gantry.PendingResult{
		Pending: []gantry.ToolCall{{ID: "leaf1", Name: "ask_user", Input: json.RawMessage(`{}`)}},
		Resume:  json.RawMessage(`{"step":1}`),
	}
	r.Add(&fakeResumable{
		def:       gantry.ToolDef{Name: "resumable", Description: "d", Schema: json.RawMessage(`{}`)},
		invokeErr: want,
	})

	_, err := r.Invoke(context.Background(), gantry.ToolCall{Name: "resumable", Input: json.RawMessage(`{}`)})
	if err == nil {
		t.Fatal("expected an error (the PendingResult)")
	}
	if errors.Is(err, gantry.ErrToolExecution) {
		t.Error("a pending call must NOT wrap ErrToolExecution — it isn't an execution failure")
	}
	var got *gantry.PendingResult
	if !errors.As(err, &got) || got != want {
		t.Errorf("errors.As did not recover the original *PendingResult; got %#v", got)
	}
}

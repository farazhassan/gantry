package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/harness"
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
	_, err := r.Invoke(context.Background(), harness.ToolCall{Name: "ghost", Input: json.RawMessage(`{}`)})
	if err == nil {
		t.Errorf("expected error invoking unknown tool")
	}
	if !errors.Is(err, harness.ErrToolExecution) {
		t.Errorf("err should wrap ErrToolExecution; got %v", err)
	}
}

func TestRegistryInvokeSuccess(t *testing.T) {
	r := tool.NewRegistry()
	r.Add(echoTool{})
	out, err := r.Invoke(context.Background(), harness.ToolCall{Name: "echo", Input: json.RawMessage(`{"a":1}`)})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if string(out) != `{"a":1}` {
		t.Errorf("Invoke returned %q", string(out))
	}
}

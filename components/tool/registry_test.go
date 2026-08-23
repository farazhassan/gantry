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

// TestRegistryInvokeTypedNilPendingResultBecomesToolErrorNotPanic guards
// against a classic Go footgun: a buggy tool can do
// `var pr *gantry.PendingResult; return output, pr`, returning a non-nil
// error interface value that wraps a nil *PendingResult pointer.
// errors.As(err, &pending) still matches and sets pending to that nil
// pointer; treating it as a genuine suspend signal panics the moment
// something does len(pending.Pending). Invoke must recognize this and fall
// through to the normal ErrToolExecution path instead.
func TestRegistryInvokeTypedNilPendingResultBecomesToolErrorNotPanic(t *testing.T) {
	r := tool.NewRegistry()
	var nilPending *gantry.PendingResult
	r.Add(&fakeResumable{
		def:       gantry.ToolDef{Name: "buggy", Description: "d", Schema: json.RawMessage(`{}`)},
		invokeErr: nilPending,
	})

	out, err := r.Invoke(context.Background(), gantry.ToolCall{Name: "buggy", Input: json.RawMessage(`{}`)})
	if err == nil {
		t.Fatal("expected a normal tool error, got nil")
	}
	if !errors.Is(err, gantry.ErrToolExecution) {
		t.Errorf("err should wrap ErrToolExecution (typed-nil PendingResult is not a real suspend signal); got %v", err)
	}
	var got *gantry.PendingResult
	if errors.As(err, &got) && got != nil {
		t.Errorf("errors.As should not recover a non-nil *PendingResult from this error; got %#v", got)
	}
	_ = out
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

// TestRegistryInvokeTypedNilPendingResultBreaksErrorsAsChain is a stricter
// companion to TestRegistryInvokeTypedNilPendingResultBecomesToolErrorNotPanic
// above: it pins down exactly which of the two possible safe outcomes
// Registry.Invoke produces for a tool that buggily returns a typed-nil
// *gantry.PendingResult (`var pr *gantry.PendingResult; return out, pr`) —
// errors.As on the returned error must be false entirely, not merely "true
// but nil". Wrapping the original err with %w (the pre-fix behavior) would
// let errors.As keep matching *gantry.PendingResult all the way through, so
// any downstream errors.As(returnedErr, &pending) call would still recover a
// nil pending and panic on first field access. Breaking the match here is
// what makes the defense-in-depth pending != nil guards elsewhere
// (middleware.go, client.go, run_stream.go) belt-and-suspenders rather than
// load-bearing.
func TestRegistryInvokeTypedNilPendingResultBreaksErrorsAsChain(t *testing.T) {
	r := tool.NewRegistry()
	var nilPending *gantry.PendingResult
	r.Add(&fakeResumable{
		def:       gantry.ToolDef{Name: "buggy_nil", Description: "d", Schema: json.RawMessage(`{}`)},
		invokeErr: nilPending,
	})

	_, err := r.Invoke(context.Background(), gantry.ToolCall{Name: "buggy_nil", Input: json.RawMessage(`{}`)})
	if err == nil {
		t.Fatal("expected a non-nil error, got nil")
	}
	if !errors.Is(err, gantry.ErrToolExecution) {
		t.Errorf("err should wrap gantry.ErrToolExecution; got %v", err)
	}

	var pending *gantry.PendingResult
	if errors.As(err, &pending) {
		t.Errorf("errors.As(err, &pending) = true, want false: the returned error must not remain matchable as *gantry.PendingResult (pending=%#v)", pending)
	}
}

// TestRegistryInvokePreservesToolSentinelThroughFallback guards the sibling
// risk introduced while fixing the typed-nil case above: the fallback
// branch in Registry.Invoke must only special-case the "matched but nil"
// PendingResult case, not every non-pending error. A tool returning an
// ordinary sentinel error (e.g. gantry.ErrToolAuth) must still be reachable
// via errors.Is on the returned error, exactly as before —
// TestRegistryInvokePreservesToolSentinel already covers this; this is a
// second, differently-named copy tied directly to this fix so a future edit
// to the fallback's error-wrapping verb doesn't silently regress it again.
func TestRegistryInvokePreservesToolSentinelThroughFallback(t *testing.T) {
	r := tool.NewRegistry()
	r.Add(authFailingTool{})
	_, err := r.Invoke(context.Background(), gantry.ToolCall{Name: "auth_failing", Input: json.RawMessage(`{}`)})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, gantry.ErrToolExecution) {
		t.Errorf("err should wrap ErrToolExecution; got %v", err)
	}
	if !errors.Is(err, gantry.ErrToolAuth) {
		t.Errorf("err should still wrap the tool's own ErrToolAuth via errors.Is; got %v", err)
	}
}

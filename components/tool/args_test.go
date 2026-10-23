package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/eval"
)

// badJSON is the shape that motivated validation: an unescaped quote inside
// a string value, as some models emit in tool-call arguments.
const badJSON = `{"header":"say "hi"","text":"t"}`

// countingTool records how many times Invoke ran.
type countingTool struct{ calls atomic.Int32 }

func (*countingTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{Name: "count", Description: "counts", Schema: json.RawMessage(`{}`)}
}

func (c *countingTool) Invoke(context.Context, json.RawMessage) (json.RawMessage, error) {
	c.calls.Add(1)
	return json.RawMessage(`"ok"`), nil
}

// toolMessage returns the RoleTool message for callID in msgs.
func toolMessage(t *testing.T, msgs []gantry.Message, callID string) gantry.Message {
	t.Helper()
	for _, m := range msgs {
		if m.Role == gantry.RoleTool && m.ToolCallID == callID {
			return m
		}
	}
	t.Fatalf("no tool message for %s in %#v", callID, msgs)
	return gantry.Message{}
}

func TestValidateArgs(t *testing.T) {
	for _, in := range []string{"", `{}`, `5`, `null`, `{"a":"b \"c\""}`} {
		if err := tool.ValidateArgs(json.RawMessage(in)); err != nil {
			t.Errorf("ValidateArgs(%q) = %v, want nil", in, err)
		}
	}
	for _, in := range []string{badJSON, `{"a":`, `{a:1}`} {
		err := tool.ValidateArgs(json.RawMessage(in))
		if !errors.Is(err, gantry.ErrToolInvalidArgs) {
			t.Errorf("ValidateArgs(%q) = %v, want ErrToolInvalidArgs", in, err)
			continue
		}
		if err.Error() != tool.InvalidJSONContent {
			t.Errorf("message = %q, want %q", err.Error(), tool.InvalidJSONContent)
		}
	}
}

func TestInvalidArgsErrorKeepsMessageAndCause(t *testing.T) {
	cause := errors.New("header too long")
	err := tool.InvalidArgsError(cause)
	if err.Error() != "header too long" || !errors.Is(err, cause) || !errors.Is(err, gantry.ErrToolInvalidArgs) {
		t.Fatalf("InvalidArgsError(cause) = %v; want same message, matching cause and ErrToolInvalidArgs", err)
	}
	if tool.InvalidArgsError(nil) != nil {
		t.Fatalf("InvalidArgsError(nil) must be nil")
	}
}

func TestRegistryInvokeRejectsInvalidJSONWithoutInvoking(t *testing.T) {
	ct := &countingTool{}
	reg := tool.NewRegistry()
	reg.Add(ct)
	_, err := reg.Invoke(context.Background(), gantry.ToolCall{ID: "c1", Name: "count", Input: json.RawMessage(badJSON)})
	if !errors.Is(err, gantry.ErrToolInvalidArgs) || !errors.Is(err, gantry.ErrToolExecution) {
		t.Fatalf("err = %v, want ErrToolInvalidArgs and ErrToolExecution", err)
	}
	if n := ct.calls.Load(); n != 0 {
		t.Fatalf("tool invoked %d times, want 0", n)
	}
}

func TestDispatchFeedsInvalidArgsBackToModel(t *testing.T) {
	ct := &countingTool{}
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "count", Input: json.RawMessage(badJSON)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c2", Name: "count", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.FromTools(1, ct)); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.FinalOutput != "final" {
		t.Fatalf("FinalOutput = %q, want final", state.FinalOutput)
	}
	if n := ct.calls.Load(); n != 1 {
		t.Fatalf("tool invoked %d times, want 1 (only the corrected call)", n)
	}
	m := toolMessage(t, mock.Requests()[1].Messages, "c1")
	if !m.IsError || m.Content != tool.InvalidJSONContent {
		t.Fatalf("c1 result = %+v, want error %q", m, tool.InvalidJSONContent)
	}
}

func TestDispatchInvalidArgsBypassesStopPolicy(t *testing.T) {
	ct := &countingTool{}
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "bad", Name: "count", Input: json.RawMessage(badJSON)},
				{ID: "good", Name: "count", Input: json.RawMessage(`{}`)},
			},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	policy := tool.Policy{Parallelism: 1, OnFailure: tool.StopCancelInFlight, Disposition: tool.HarnessStop}
	if err := a.With(tool.FromToolsWithPolicy(policy, ct)); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v (invalid args must not trip the stop policy)", err)
	}
	if state.DoneReason != gantry.DoneNoToolCalls {
		t.Fatalf("DoneReason = %q, want %q", state.DoneReason, gantry.DoneNoToolCalls)
	}
	if n := ct.calls.Load(); n != 1 {
		t.Fatalf("tool invoked %d times, want 1 (the sibling call must still run)", n)
	}
	msgs := mock.Requests()[1].Messages
	if m := toolMessage(t, msgs, "bad"); !m.IsError {
		t.Fatalf("bad result = %+v, want error", m)
	}
	if m := toolMessage(t, msgs, "good"); m.IsError {
		t.Fatalf("good result = %+v, want success", m)
	}
}

func TestClientInvalidJSONFedBackInsteadOfSuspending(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "q1", Name: "ask_user", Input: json.RawMessage(badJSON)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.Client(askDef())); err != nil {
		t.Fatalf("install client tools: %v", err)
	}

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.DoneReason != gantry.DoneNoToolCalls || len(state.PendingToolCalls) != 0 {
		t.Fatalf("DoneReason=%q pending=%#v, want run to continue past the rejected call", state.DoneReason, state.PendingToolCalls)
	}
	m := toolMessage(t, mock.Requests()[1].Messages, "q1")
	if !m.IsError || m.Content != tool.InvalidJSONContent {
		t.Fatalf("q1 result = %+v, want error %q", m, tool.InvalidJSONContent)
	}
}

func TestClientSuspendsOnlyOnValidCalls(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "q1", Name: "ask_user", Input: json.RawMessage(`{"q":"ok?"}`)},
				{ID: "q2", Name: "ask_user", Input: json.RawMessage(badJSON)},
			},
			StopReason: gantry.StopReasonToolUse,
		},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.Client(askDef())); err != nil {
		t.Fatalf("install client tools: %v", err)
	}

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.DoneReason != gantry.DoneClientToolCall {
		t.Fatalf("DoneReason = %q, want suspend", state.DoneReason)
	}
	if len(state.PendingToolCalls) != 1 || state.PendingToolCalls[0].ID != "q1" {
		t.Fatalf("PendingToolCalls = %#v, want only q1", state.PendingToolCalls)
	}
	if m := toolMessage(t, state.Messages, "q2"); !m.IsError {
		t.Fatalf("q2 result = %+v, want error", m)
	}
}

func TestDynamicClientInvalidJSONFedBack(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "q1", Name: "get_location", Input: json.RawMessage(`{"precise":`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.DynamicClient()); err != nil {
		t.Fatalf("install dynamic client: %v", err)
	}
	prior := &gantry.State{}
	if err := tool.SetPendingClientTools(prior, gantry.ToolDef{Name: "get_location", Schema: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("SetPendingClientTools: %v", err)
	}

	state, err := a.RunFrom(context.Background(), prior, "where am I?")
	if err != nil {
		t.Fatalf("RunFrom: %v", err)
	}
	if state.DoneReason != gantry.DoneNoToolCalls {
		t.Fatalf("DoneReason = %q, want run to continue", state.DoneReason)
	}
	if m := toolMessage(t, mock.Requests()[1].Messages, "q1"); !m.IsError || m.Content != tool.InvalidJSONContent {
		t.Fatalf("q1 result = %+v, want error %q", m, tool.InvalidJSONContent)
	}
}

func TestRejectedClientCallEmitsToolResult(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "q1", Name: "ask_user", Input: json.RawMessage(badJSON)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.Client(askDef())); err != nil {
		t.Fatalf("install client tools: %v", err)
	}
	var got *gantry.ToolResult
	_, err := a.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		if ev.Type == gantry.EventToolResult && ev.ToolResult != nil && ev.ToolResult.CallID == "q1" {
			r := *ev.ToolResult
			got = &r
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if got == nil || !got.IsError || !errors.Is(got.Err, gantry.ErrToolInvalidArgs) {
		t.Fatalf("EventToolResult for q1 = %+v, want an invalid-args error", got)
	}
}

func TestClientArgsValidatorRejects(t *testing.T) {
	requireQ := func(in json.RawMessage) error {
		if !strings.Contains(string(in), `"q"`) {
			return errors.New("ask_user: q is required")
		}
		return nil
	}
	for _, dynamic := range []bool{false, true} {
		name := "Client"
		if dynamic {
			name = "DynamicClient"
		}
		t.Run(name, func(t *testing.T) {
			mock := eval.NewMockLLMClient(
				gantry.LLMResponse{
					ToolCalls:  []gantry.ToolCall{{ID: "q1", Name: "ask_user", Input: json.RawMessage(`{}`)}},
					StopReason: gantry.StopReasonToolUse,
				},
				gantry.LLMResponse{
					ToolCalls:  []gantry.ToolCall{{ID: "q2", Name: "ask_user", Input: json.RawMessage(`{"q":"ok?"}`)}},
					StopReason: gantry.StopReasonToolUse,
				},
			)
			a, _ := gantry.NewAgent(gantry.WithLLM(mock))
			prior := &gantry.State{}
			if dynamic {
				if err := a.With(tool.DynamicClient(), tool.ClientArgsValidator("ask_user", requireQ)); err != nil {
					t.Fatalf("install: %v", err)
				}
				if err := tool.SetPendingClientTools(prior, askDef()); err != nil {
					t.Fatalf("SetPendingClientTools: %v", err)
				}
			} else if err := a.With(tool.Client(askDef()), tool.ClientArgsValidator("ask_user", requireQ)); err != nil {
				t.Fatalf("install: %v", err)
			}

			state, err := a.RunFrom(context.Background(), prior, "go")
			if err != nil {
				t.Fatalf("RunFrom: %v", err)
			}
			if state.DoneReason != gantry.DoneClientToolCall || len(state.PendingToolCalls) != 1 || state.PendingToolCalls[0].ID != "q2" {
				t.Fatalf("DoneReason=%q pending=%#v, want suspend on q2 only", state.DoneReason, state.PendingToolCalls)
			}
			m := toolMessage(t, state.Messages, "q1")
			if !m.IsError || m.Content != "ask_user: q is required" {
				t.Fatalf("q1 result = %+v, want the validator's error", m)
			}
		})
	}
}

func TestClientArgsValidatorChecksJSONFirst(t *testing.T) {
	called := false
	v := func(json.RawMessage) error { called = true; return nil }
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "q1", Name: "ask_user", Input: json.RawMessage(badJSON)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	// Validator installed first, so its middleware sees the call before the
	// JSON check Client installs.
	if err := a.With(tool.ClientArgsValidator("ask_user", v), tool.Client(askDef())); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Fatalf("validator must not see input that is not valid JSON")
	}
	if m := toolMessage(t, mock.Requests()[1].Messages, "q1"); m.Content != tool.InvalidJSONContent {
		t.Fatalf("q1 content = %q, want %q", m.Content, tool.InvalidJSONContent)
	}
}

func TestClientArgsValidatorIgnoresServerCalls(t *testing.T) {
	ct := &countingTool{}
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "count", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	reject := func(json.RawMessage) error { return errors.New("rejected") }
	if err := a.With(tool.FromTools(1, ct), tool.Client(askDef()), tool.ClientArgsValidator("count", reject)); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := ct.calls.Load(); n != 1 {
		t.Fatalf("server tool invoked %d times, want 1", n)
	}
}

func TestClientArgsValidatorInstallErrors(t *testing.T) {
	ok := func(json.RawMessage) error { return nil }
	a, _ := gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient()))
	if err := a.With(tool.ClientArgsValidator("", ok)); err == nil {
		t.Errorf("empty name: want error")
	}
	if err := a.With(tool.ClientArgsValidator("ask_user", nil)); err == nil {
		t.Errorf("nil validator: want error")
	}
	if err := a.With(tool.ClientArgsValidator("ask_user", ok)); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if err := a.With(tool.ClientArgsValidator("ask_user", ok)); err == nil {
		t.Errorf("duplicate install: want error")
	}
}

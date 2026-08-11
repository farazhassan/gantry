package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/eval"
)

type addOneTool struct{}

func (addOneTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{Name: "add_one", Description: "adds one", Schema: json.RawMessage(`{}`)}
}

func (addOneTool) Invoke(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var n int
	if err := json.Unmarshal(input, &n); err != nil {
		return nil, err
	}
	out, _ := json.Marshal(n + 1)
	return out, nil
}

func TestWithToolRegistersDefinitionAtStart(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	if err := a.With(tool.FromTools(1, addOneTool{})); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	reqs := mock.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	if len(reqs[0].Tools) != 1 || reqs[0].Tools[0].Name != "add_one" {
		t.Errorf("Tools not registered in LLM request: %+v", reqs[0].Tools)
	}
}

func TestWithToolDispatchesPendingCalls(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "add_one", Input: json.RawMessage(`5`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.FromTools(1, addOneTool{})); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.FinalOutput != "final" {
		t.Errorf("FinalOutput = %q", state.FinalOutput)
	}

	reqs := mock.Requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	found := false
	for _, m := range reqs[1].Messages {
		if m.Role == gantry.RoleTool && m.ToolCallID == "c1" && m.Content == "6" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected tool result with CallID c1 and content '6'; messages: %+v", reqs[1].Messages)
	}
}

func TestWithToolsParallelDispatch(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "a", Name: "add_one", Input: json.RawMessage(`1`)},
				{ID: "b", Name: "add_one", Input: json.RawMessage(`2`)},
				{ID: "c", Name: "add_one", Input: json.RawMessage(`3`)},
			},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.FromTools(2, addOneTool{})); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	reqs := mock.Requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	results := map[string]string{}
	for _, m := range reqs[1].Messages {
		if m.Role == gantry.RoleTool {
			results[m.ToolCallID] = m.Content
		}
	}
	for callID, want := range map[string]string{"a": "2", "b": "3", "c": "4"} {
		if results[callID] != want {
			t.Errorf("call %q result = %q, want %q", callID, results[callID], want)
		}
	}
}

func TestWithToolUnknownToolRecordsError(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "g", Name: "ghost"}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.FromTools(1, addOneTool{})); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	reqs := mock.Requests()
	found := false
	for _, m := range reqs[1].Messages {
		if m.Role == gantry.RoleTool && m.ToolCallID == "g" {
			found = true
			if m.Content == "" {
				t.Errorf("expected error content in tool result")
			}
		}
	}
	if !found {
		t.Errorf("missing tool result for unknown tool")
	}
}

// TestFromToolsDefaultPolicyKeepsGoingAndFeedsFailureBackToLLM exercises the
// zero-value Policy default (KeepGoing + FeedbackToLLM) through the plain
// public tool.FromTools API — the path every existing caller of tool.New /
// tool.FromTools exercises unchanged by this feature. A failing call must
// not abort the run, and its successful sibling in the same batch must still
// run to completion and have its result fed back to the LLM. Unlike
// TestWithToolUnknownToolRecordsError (a single unknown-tool call with no
// sibling), this proves the multi-call batch-failure default explicitly.
func TestFromToolsDefaultPolicyKeepsGoingAndFeedsFailureBackToLLM(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "fail", Name: "boom", Input: json.RawMessage(`{}`)},
				{ID: "ok", Name: "add_one", Input: json.RawMessage(`5`)},
			},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.FromTools(2, failingTool{name: "boom", err: errors.New("boom")}, addOneTool{})); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v (default policy must not abort the run on a tool failure)", err)
	}
	if state.DoneReason == gantry.DoneToolPolicyAborted {
		t.Errorf("DoneReason = %q, want a normal completion under the default policy", state.DoneReason)
	}

	reqs := mock.Requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2 (run should continue past the failure to a second LLM turn)", len(reqs))
	}
	var okResult string
	var okSeen, failSeen bool
	for _, m := range reqs[1].Messages {
		if m.Role != gantry.RoleTool {
			continue
		}
		switch m.ToolCallID {
		case "ok":
			okSeen = true
			okResult = m.Content
		case "fail":
			failSeen = true
		}
	}
	if !okSeen || okResult != "6" {
		t.Errorf("sibling 'ok' result seen=%v content=%q, want seen=true content=%q", okSeen, okResult, "6")
	}
	if !failSeen {
		t.Errorf("expected an error result recorded for the failing call 'fail' too")
	}
}

// addTwoTool is a second tool used to prove runtime reg.Add is visible.
type addTwoTool struct{}

func (addTwoTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{Name: "add_two", Description: "adds two", Schema: json.RawMessage(`{}`)}
}

func (addTwoTool) Invoke(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
	var n int
	if err := json.Unmarshal(input, &n); err != nil {
		return nil, err
	}
	out, _ := json.Marshal(n + 2)
	return out, nil
}

func TestWithRegistryHappyPath(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	reg := tool.NewRegistry()
	reg.Add(addOneTool{})
	if err := a.With(tool.New(reg, 1)); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	reqs := mock.Requests()
	if len(reqs) != 1 || len(reqs[0].Tools) != 1 || reqs[0].Tools[0].Name != "add_one" {
		t.Errorf("registry tools not advertised: %+v", reqs)
	}
}

func TestWithRegistrySharedAcrossAgents(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(addOneTool{})

	mockA := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	mockB := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mockA))
	b, _ := gantry.NewAgent(gantry.WithLLM(mockB))

	if err := a.With(tool.New(reg, 1)); err != nil {
		t.Fatalf("install tool on a: %v", err)
	}
	if err := b.With(tool.New(reg, 1)); err != nil {
		t.Fatalf("install tool on b: %v", err)
	}

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run a: %v", err)
	}
	if _, err := b.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run b: %v", err)
	}

	reqA := mockA.Requests()
	if len(reqA) != 1 || len(reqA[0].Tools) != 1 || reqA[0].Tools[0].Name != "add_one" {
		t.Errorf("agent a: shared registry tool not visible: %+v", reqA)
	}
	reqB := mockB.Requests()
	if len(reqB) != 1 || len(reqB[0].Tools) != 1 || reqB[0].Tools[0].Name != "add_one" {
		t.Errorf("agent b: shared registry tool not visible: %+v", reqB)
	}
}

func TestWithRegistryRuntimeAddVisibleNextRun(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(addOneTool{})

	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd},
		gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.New(reg, 1)); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	if _, err := a.Run(context.Background(), "first"); err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	reg.Add(addTwoTool{}) // mutate the registry between runs
	if _, err := a.Run(context.Background(), "second"); err != nil {
		t.Fatalf("Run 2: %v", err)
	}

	reqs := mock.Requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	if len(reqs[0].Tools) != 1 {
		t.Errorf("run 1 should advertise 1 tool, got %+v", reqs[0].Tools)
	}
	names := map[string]bool{}
	for _, td := range reqs[1].Tools {
		names[td.Name] = true
	}
	if !names["add_one"] || !names["add_two"] {
		t.Errorf("run 2 should advertise add_one and add_two, got %+v", reqs[1].Tools)
	}
}

func TestToolDoubleInstallReturnsError(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	reg := tool.NewRegistry()
	reg.Add(addOneTool{})
	if err := a.With(tool.New(reg, 1)); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if err := a.With(tool.New(reg, 1)); err == nil {
		t.Fatal("second install: want error, got nil")
	}
}

// callIDRecorderTool records the ToolCall.ID it observed via
// tool.CallIDFrom(ctx) during Invoke, guarded by a mutex since dispatch may
// run tools concurrently.
type callIDRecorderTool struct {
	mu   sync.Mutex
	seen string
	ok   bool
}

func (*callIDRecorderTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{Name: "record_call_id", Description: "records the invoking call ID", Schema: json.RawMessage(`{}`)}
}

func (r *callIDRecorderTool) Invoke(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	id, ok := tool.CallIDFrom(ctx)
	r.mu.Lock()
	r.seen, r.ok = id, ok
	r.mu.Unlock()
	return json.RawMessage(`"ok"`), nil
}

func TestWithToolDispatchSetsCallIDInContext(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "call-known-1", Name: "record_call_id", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	recorder := &callIDRecorderTool{}
	if err := a.With(tool.FromTools(1, recorder)); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if !recorder.ok || recorder.seen != "call-known-1" {
		t.Errorf("CallIDFrom observed = (%q, %v), want (call-known-1, true)", recorder.seen, recorder.ok)
	}
}

func TestFromToolsDoubleInstallReturnsError(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	if err := a.With(tool.FromTools(1, addOneTool{})); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if err := a.With(tool.FromTools(1, addOneTool{})); err == nil {
		t.Fatal("second install: want error, got nil")
	}
}

func TestNewWithPolicyDispatchesPendingCalls(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "add_one", Input: json.RawMessage(`5`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	reg := tool.NewRegistry()
	reg.Add(addOneTool{})
	if err := a.With(tool.NewWithPolicy(reg, tool.Policy{Parallelism: 1})); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.FinalOutput != "final" {
		t.Errorf("FinalOutput = %q", state.FinalOutput)
	}
}

type failingTool struct {
	name string
	err  error
}

func (t failingTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{Name: t.name, Description: "always fails", Schema: json.RawMessage(`{}`)}
}

func (t failingTool) Invoke(context.Context, json.RawMessage) (json.RawMessage, error) {
	return nil, t.err
}

func TestPolicyStopWaitInFlightSkipsQueuedCalls(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "fail", Name: "boom", Input: json.RawMessage(`{}`)},
				{ID: "ok", Name: "add_one", Input: json.RawMessage(`5`)},
			},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	policy := tool.Policy{Parallelism: 1, OnFailure: tool.StopWaitInFlight}
	if err := a.With(tool.FromToolsWithPolicy(policy, failingTool{name: "boom", err: errors.New("boom")}, addOneTool{})); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	reqs := mock.Requests()
	var okResult string
	for _, m := range reqs[1].Messages {
		if m.Role == gantry.RoleTool && m.ToolCallID == "ok" {
			okResult = m.Content
		}
	}
	if okResult != gantry.ErrToolSkipped.Error() {
		t.Errorf("call %q result = %q, want skipped (%q)", "ok", okResult, gantry.ErrToolSkipped.Error())
	}
}

type gatedTool struct {
	started chan struct{}
	release chan struct{}
}

func (gatedTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{Name: "gated", Description: "blocks until released", Schema: json.RawMessage(`{}`)}
}

func (g gatedTool) Invoke(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	close(g.started)
	select {
	case <-g.release:
		return json.RawMessage(`"released"`), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// waitThenFailTool wraps failingTool but blocks until wait is closed before
// returning its error. Parallelism-2 dispatch launches both the failing call
// and gatedTool's call concurrently with no ordering guarantee between them;
// since failingTool.Invoke would otherwise return near-instantly, the
// dispatcher's "aborted" flag could flip before gatedTool's job even reaches
// its own Invoke call, causing it to be skipped instead of proving it runs
// while genuinely in flight. Gating the failure on gatedTool's own "started"
// signal removes that race deterministically, without a sleep.
type waitThenFailTool struct {
	failingTool
	wait <-chan struct{}
}

func (t waitThenFailTool) Invoke(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	<-t.wait
	return t.failingTool.Invoke(ctx, input)
}

func TestPolicyStopWaitInFlightLetsRunningCallFinish(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "fail", Name: "boom", Input: json.RawMessage(`{}`)},
				{ID: "gated", Name: "gated", Input: json.RawMessage(`{}`)},
			},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	g := gatedTool{started: make(chan struct{}), release: make(chan struct{})}
	boom := waitThenFailTool{failingTool: failingTool{name: "boom", err: errors.New("boom")}, wait: g.started}
	policy := tool.Policy{Parallelism: 2, OnFailure: tool.StopWaitInFlight}
	if err := a.With(tool.FromToolsWithPolicy(policy, boom, g)); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := a.Run(context.Background(), "go")
		done <- err
	}()

	select {
	case <-g.started:
	case <-time.After(2 * time.Second):
		t.Fatal("gated tool never started")
	}
	close(g.release)

	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}

	reqs := mock.Requests()
	for _, m := range reqs[1].Messages {
		// gatedTool.Invoke returns the JSON string `"released"` (quotes
		// included, since dispatch stores Content as the raw JSON payload
		// verbatim), so the success case must compare against the quoted
		// form, not the bare word.
		if m.Role == gantry.RoleTool && m.ToolCallID == "gated" && m.Content != `"released"` {
			t.Errorf("gated call result = %q, want %q (should finish naturally, not be cancelled or skipped)", m.Content, `"released"`)
		}
	}
}

type ctxAwareTool struct {
	// started is closed on entry to Invoke, before the select below. It lets
	// a paired failing call in the same batch be gated on this call having
	// genuinely begun, closing the same race waitThenFailTool closes for
	// gatedTool (see its comment): without it, a fast-failing sibling
	// dispatched concurrently could flip the dispatcher's "aborted" flag
	// before this call's job even reaches its own Invoke, so it would never
	// run at all rather than being observed in flight and cancelled.
	started chan struct{}
	result  chan bool // true if ctx was cancelled before the long timeout
}

func (ctxAwareTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{Name: "ctx_aware", Description: "reports whether ctx was cancelled", Schema: json.RawMessage(`{}`)}
}

func (c ctxAwareTool) Invoke(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	close(c.started)
	select {
	case <-ctx.Done():
		c.result <- true
		return json.RawMessage(`"cancelled"`), nil
	case <-time.After(2 * time.Second):
		c.result <- false
		return json.RawMessage(`"not-cancelled"`), nil
	}
}

func TestPolicyStopCancelInFlightCancelsRunningCalls(t *testing.T) {
	// OnFailure=StopCancelInFlight only affects sibling calls; Disposition
	// defaults to FeedbackToLLM, so the run continues to a second LLM turn —
	// the mock script needs a response for it.
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "fail", Name: "boom", Input: json.RawMessage(`{}`)},
				{ID: "aware", Name: "ctx_aware", Input: json.RawMessage(`{}`)},
			},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	aware := ctxAwareTool{started: make(chan struct{}), result: make(chan bool, 1)}
	boom := waitThenFailTool{failingTool: failingTool{name: "boom", err: errors.New("boom")}, wait: aware.started}
	policy := tool.Policy{Parallelism: 2, OnFailure: tool.StopCancelInFlight}
	if err := a.With(tool.FromToolsWithPolicy(policy, boom, aware)); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	select {
	case cancelled := <-aware.result:
		if !cancelled {
			t.Errorf("ctx_aware call was not cancelled")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ctx_aware tool never reported a result")
	}
}

func TestPolicyHarnessStopAbortsRunButKeepGoingStillRunsSiblings(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "fail", Name: "boom", Input: json.RawMessage(`{}`)},
				{ID: "ok", Name: "add_one", Input: json.RawMessage(`5`)},
			},
			StopReason: gantry.StopReasonToolUse,
		},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	policy := tool.Policy{Parallelism: 2, OnFailure: tool.KeepGoing, Disposition: tool.HarnessStop}
	if err := a.With(tool.FromToolsWithPolicy(policy, failingTool{name: "boom", err: errors.New("boom")}, addOneTool{})); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	state, err := a.Run(context.Background(), "go")
	if err == nil || !errors.Is(err, gantry.ErrToolPolicyAborted) {
		t.Fatalf("Run err = %v, want ErrToolPolicyAborted", err)
	}
	if state.DoneReason != gantry.DoneToolPolicyAborted {
		t.Errorf("DoneReason = %q, want %q", state.DoneReason, gantry.DoneToolPolicyAborted)
	}
	found := false
	for _, r := range state.ToolResults {
		if r.CallID == "ok" && !r.IsError {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the sibling 'ok' call to have run to completion under OnFailure=KeepGoing; results: %+v", state.ToolResults)
	}
}

func TestPolicyPersistentErrorForcesCancelAndHarnessStop(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "fail", Name: "boom", Input: json.RawMessage(`{}`)},
				{ID: "aware", Name: "ctx_aware", Input: json.RawMessage(`{}`)},
			},
			StopReason: gantry.StopReasonToolUse,
		},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	aware := ctxAwareTool{started: make(chan struct{}), result: make(chan bool, 1)}
	authErr := fmt.Errorf("%w: token expired", gantry.ErrToolAuth)
	boom := waitThenFailTool{failingTool: failingTool{name: "boom", err: authErr}, wait: aware.started}
	// Policy{} is the zero value: KeepGoing + FeedbackToLLM. The persistent
	// sentinel must override both.
	if err := a.With(tool.FromToolsWithPolicy(tool.Policy{Parallelism: 2}, boom, aware)); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	state, err := a.Run(context.Background(), "go")
	if err == nil || !errors.Is(err, gantry.ErrToolPolicyAborted) {
		t.Fatalf("Run err = %v, want ErrToolPolicyAborted", err)
	}
	if !errors.Is(err, gantry.ErrToolAuth) {
		t.Errorf("Run err = %v, want errors.Is(err, gantry.ErrToolAuth) to hold (the triggering sentinel must survive the ErrToolPolicyAborted wrap)", err)
	}
	if state.DoneReason != gantry.DoneToolPolicyAborted {
		t.Errorf("DoneReason = %q, want %q", state.DoneReason, gantry.DoneToolPolicyAborted)
	}

	select {
	case cancelled := <-aware.result:
		if !cancelled {
			t.Errorf("ctx_aware call was not cancelled despite persistent-error override")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ctx_aware tool never reported a result")
	}
}

// TestPolicyLargeFanOutPersistentErrorLeavesNoZeroValueResults is a
// regression test for a bug where gantry.RunParallel (workers.go, not
// modified here) can abandon queued jobs entirely rather than ever invoking
// them: its per-job dispatch loop does
//
//	select {
//	case <-ctx.Done():
//	        ... return ...
//	case sem <- struct{}{}:
//	}
//
// one job at a time, in order. If a call's job closure fails fast with a
// persistent error (gantry.ErrToolAuth / gantry.ErrToolPersistent), the
// automatic override fires cancel() on the shared execCtx synchronously
// before that closure returns — strictly before RunParallel's own deferred
// semaphore release for that job even runs. So by the time the dispatch
// loop reaches its next not-yet-launched job, ctx.Done() is already closed,
// and the loop takes that branch instead of ever launching the job (or any
// job still left in the slice after it) — the job's closure body,
// including its own aborted.Load() skip path, never runs at all. Before the
// fix, the corresponding results[i] was only ever written from inside that
// closure, so an abandoned call's entry was left at Go's zero value
// (ToolResult{CallID: "", ...}), which would have produced a
// Role: RoleTool message with no ToolCallID — a 1:1 tool_use/tool_result
// correspondence violation against real LLM providers.
//
// This is reproduced deterministically (not by relying on scheduler luck
// with full parallelism, which only reproduces the bug probabilistically
// for a coin-flip's worth of runs) by using bounded parallelism with the
// slots ahead of the queue held open by calls that are themselves
// cancellation-aware: with Parallelism == numHeld+1, the failing call and
// numHeld ctxAwareTool "occupier" calls are the only ones RunParallel's
// dispatch loop can hand a semaphore slot to before the queue backs up.
// Every occupier call blocks until it observes ctx.Done() (so it never
// finishes on its own and steals a freed slot out from under the queue),
// which guarantees at least one of the many still-queued calls behind them
// is genuinely parked in RunParallel's select at the moment cancel() fires
// — and once that first queued call's iteration takes the ctx.Done()
// branch, gantry.RunParallel abandons every remaining call in the slice
// unconditionally, regardless of scheduler timing.
func TestPolicyLargeFanOutPersistentErrorLeavesNoZeroValueResults(t *testing.T) {
	const numHeld = 2    // occupier calls that hold every semaphore slot open
	const numQueued = 20 // calls guaranteed to be abandoned by RunParallel

	calls := []gantry.ToolCall{
		{ID: "fail", Name: "boom", Input: json.RawMessage(`{}`)},
	}
	occupiers := make([]ctxAwareTool, numHeld)
	for i := 0; i < numHeld; i++ {
		occupiers[i] = ctxAwareTool{started: make(chan struct{}), result: make(chan bool, 1)}
		calls = append(calls, gantry.ToolCall{
			ID:    fmt.Sprintf("occupier%d", i),
			Name:  fmt.Sprintf("occupier%d", i),
			Input: json.RawMessage(`{}`),
		})
	}
	for i := 0; i < numQueued; i++ {
		calls = append(calls, gantry.ToolCall{
			ID:    fmt.Sprintf("ok%d", i),
			Name:  "add_one",
			Input: json.RawMessage(fmt.Sprintf("%d", i)),
		})
	}

	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{ToolCalls: calls, StopReason: gantry.StopReasonToolUse},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	reg := tool.NewRegistry()
	authErr := fmt.Errorf("%w: token expired", gantry.ErrToolAuth)
	reg.Add(failingTool{name: "boom", err: authErr})
	reg.Add(addOneTool{})
	for i, occ := range occupiers {
		// Each occupier needs its own tool name since Registry is keyed by
		// name; wrap ctxAwareTool to answer under a distinct Definition().
		reg.Add(namedTool{Tool: occ, name: fmt.Sprintf("occupier%d", i)})
	}
	if err := a.With(tool.NewWithPolicy(reg, tool.Policy{Parallelism: numHeld + 1})); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	state, err := a.Run(context.Background(), "go")
	if err == nil || !errors.Is(err, gantry.ErrToolPolicyAborted) {
		t.Fatalf("Run err = %v, want ErrToolPolicyAborted", err)
	}
	if state.DoneReason != gantry.DoneToolPolicyAborted {
		t.Errorf("DoneReason = %q, want %q", state.DoneReason, gantry.DoneToolPolicyAborted)
	}

	if len(state.ToolResults) != len(calls) {
		t.Fatalf("len(ToolResults) = %d, want %d (one result per pending call, none dropped from the slice)", len(state.ToolResults), len(calls))
	}
	for i, r := range state.ToolResults {
		if r.CallID == "" {
			t.Errorf("ToolResults[%d] = %+v: empty CallID means a zero-value ToolResult slipped through for a call RunParallel abandoned", i, r)
		}
	}
}

// namedTool lets a single Tool value be registered under a caller-chosen
// name, so distinct ctxAwareTool instances (each with their own started/
// result channels) can be individually addressed by ToolCall.Name.
type namedTool struct {
	tool.Tool
	name string
}

func (n namedTool) Definition() gantry.ToolDef {
	d := n.Tool.Definition()
	d.Name = n.name
	return d
}

func TestDispatchEmitsLiveResultEventPerCall(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "a", Name: "add_one", Input: json.RawMessage(`1`)},
				{ID: "b", Name: "add_one", Input: json.RawMessage(`2`)},
			},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.FromTools(2, addOneTool{})); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	var events []gantry.Event
	if _, err := a.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		events = append(events, ev)
		return nil
	}); err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	liveEvents := map[string]gantry.Event{}
	liveIdx := map[string]int{}
	batchedIdx := -1
	for i, ev := range events {
		if ev.Type == gantry.EventToolResultLive && ev.ToolResult != nil {
			liveEvents[ev.ToolResult.CallID] = ev
			liveIdx[ev.ToolResult.CallID] = i
		}
		if ev.Type == gantry.EventToolResult && batchedIdx == -1 {
			batchedIdx = i
		}
	}

	if len(liveEvents) != 2 {
		t.Fatalf("got %d live tool_result_live events, want 2: %+v", len(liveEvents), liveEvents)
	}
	if liveEvents["a"].ToolResult.Content != "2" || liveEvents["b"].ToolResult.Content != "3" {
		t.Errorf("live results: a=%+v b=%+v, want a.Content=2 b.Content=3", liveEvents["a"].ToolResult, liveEvents["b"].ToolResult)
	}
	for id, ev := range liveEvents {
		if ev.Timestamp.IsZero() {
			t.Errorf("live event for %q has zero Timestamp, want it stamped by emit()", id)
		}
	}
	if batchedIdx == -1 {
		t.Fatal("expected at least one batched tool_result event")
	}
	for id, idx := range liveIdx {
		if idx >= batchedIdx {
			t.Errorf("live event for %q at index %d did not precede the batched tool_result event at index %d", id, idx, batchedIdx)
		}
	}
}

func TestDispatchEmitsLiveResultEventForSkippedCall(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "fail", Name: "boom", Input: json.RawMessage(`{}`)},
				{ID: "ok", Name: "add_one", Input: json.RawMessage(`5`)},
			},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	policy := tool.Policy{Parallelism: 1, OnFailure: tool.StopWaitInFlight}
	if err := a.With(tool.FromToolsWithPolicy(policy, failingTool{name: "boom", err: errors.New("boom")}, addOneTool{})); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	var sawSkippedLive bool
	if _, err := a.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		if ev.Type == gantry.EventToolResultLive && ev.ToolResult != nil && ev.ToolResult.CallID == "ok" {
			if ev.ToolResult.IsError && ev.ToolResult.Content == gantry.ErrToolSkipped.Error() {
				sawSkippedLive = true
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	if !sawSkippedLive {
		t.Error("expected a live tool_result_live event for the skipped 'ok' call")
	}
}

func TestDispatchLiveResultEventSinkErrorDoesNotAbortRun(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "a", Name: "add_one", Input: json.RawMessage(`1`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.FromTools(1, addOneTool{})); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	boom := errors.New("sink boom")
	state, err := a.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		if ev.Type == gantry.EventToolResultLive {
			return boom
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunStream: %v, want nil (live event sink errors must be discarded)", err)
	}
	if state.FinalOutput != "done" {
		t.Errorf("FinalOutput = %q, want run to complete normally", state.FinalOutput)
	}
}

func TestDispatchLiveResultEventsAreSerialized(t *testing.T) {
	calls := make([]gantry.ToolCall, 8)
	for i := range calls {
		calls[i] = gantry.ToolCall{ID: fmt.Sprintf("c%d", i), Name: "add_one", Input: json.RawMessage(fmt.Sprintf("%d", i))}
	}
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{ToolCalls: calls, StopReason: gantry.StopReasonToolUse},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.FromTools(8, addOneTool{})); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	// Deliberately NOT synchronized: if components/tool ever emits
	// EventToolResultLive concurrently from more than one goroutine, `go
	// test -race` will flag this unguarded append as a data race.
	var got []gantry.Event
	if _, err := a.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		got = append(got, ev)
		return nil
	}); err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	liveCount := 0
	for _, ev := range got {
		if ev.Type == gantry.EventToolResultLive {
			liveCount++
		}
	}
	if liveCount != len(calls) {
		t.Errorf("got %d live events, want %d", liveCount, len(calls))
	}
}

// TestDispatchLiveResultEventReflectsCancelledCallOutcome proves that when
// OnFailure: StopCancelInFlight cancels a call already in flight, the
// EventToolResultLive emitted for that call carries its real outcome from
// the tool's own Invoke (here, ctxAwareTool observing ctx.Done() and
// returning `"cancelled"`, IsError: false) rather than a fabricated
// gantry.ErrToolSkipped — skip is only for calls that never started at all,
// not ones that were running and responded to cancellation themselves.
//
// boom is wrapped in waitThenFailTool, gated on aware.started, for the same
// reason TestPolicyStopCancelInFlightCancelsRunningCalls does: without it, a
// fast-failing sibling dispatched concurrently could flip the dispatcher's
// "aborted" flag (and fire cancel()) before ctx_aware's job even reaches its
// own Invoke, so the "in flight when cancelled" scenario this test targets
// would not reliably occur.
func TestDispatchLiveResultEventReflectsCancelledCallOutcome(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "fail", Name: "boom", Input: json.RawMessage(`{}`)},
				{ID: "aware", Name: "ctx_aware", Input: json.RawMessage(`{}`)},
			},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	aware := ctxAwareTool{started: make(chan struct{}), result: make(chan bool, 1)}
	boom := waitThenFailTool{failingTool: failingTool{name: "boom", err: errors.New("boom")}, wait: aware.started}
	policy := tool.Policy{Parallelism: 2, OnFailure: tool.StopCancelInFlight}
	if err := a.With(tool.FromToolsWithPolicy(policy, boom, aware)); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	var awareLive *gantry.Event
	if _, err := a.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		if ev.Type == gantry.EventToolResultLive && ev.ToolResult != nil && ev.ToolResult.CallID == "aware" {
			e := ev
			awareLive = &e
		}
		return nil
	}); err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	select {
	case cancelled := <-aware.result:
		if !cancelled {
			t.Errorf("ctx_aware call was not cancelled")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ctx_aware tool never reported a result")
	}

	if awareLive == nil {
		t.Fatal("expected a live tool_result_live event for the cancelled 'aware' call")
	}
	if awareLive.ToolResult.IsError || awareLive.ToolResult.Content != `"cancelled"` {
		t.Errorf("aware live result = %+v, want IsError=false Content=\"cancelled\" (ctx_aware.Invoke's real outcome, not a fabricated skip)", awareLive.ToolResult)
	}
}

// TestDispatchLiveResultEventCoversAbandonedQueuedCalls is a regression test
// for a gap flagged in PR review: emitLive is only ever called from inside a
// job closure, but gantry.RunParallel's dispatch loop can abandon a queued
// job the instant execCtx is cancelled (see the comment above the results[i]
// pre-fill), without ever invoking that job's closure — not even its
// aborted.Load() skip path. Before the fix, such a call never got any
// EventToolResultLive at all; only the eventual batched EventToolResult
// would mention it. This reuses the deterministic
// occupier-holds-every-semaphore-slot pattern from
// TestPolicyLargeFanOutPersistentErrorLeavesNoZeroValueResults to force
// real, reproducible abandonment rather than relying on scheduler timing.
func TestDispatchLiveResultEventCoversAbandonedQueuedCalls(t *testing.T) {
	const numHeld = 2
	const numQueued = 20

	calls := []gantry.ToolCall{
		{ID: "fail", Name: "boom", Input: json.RawMessage(`{}`)},
	}
	occupiers := make([]ctxAwareTool, numHeld)
	for i := 0; i < numHeld; i++ {
		occupiers[i] = ctxAwareTool{started: make(chan struct{}), result: make(chan bool, 1)}
		calls = append(calls, gantry.ToolCall{
			ID:    fmt.Sprintf("occupier%d", i),
			Name:  fmt.Sprintf("occupier%d", i),
			Input: json.RawMessage(`{}`),
		})
	}
	for i := 0; i < numQueued; i++ {
		calls = append(calls, gantry.ToolCall{
			ID:    fmt.Sprintf("ok%d", i),
			Name:  "add_one",
			Input: json.RawMessage(fmt.Sprintf("%d", i)),
		})
	}

	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{ToolCalls: calls, StopReason: gantry.StopReasonToolUse},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	reg := tool.NewRegistry()
	authErr := fmt.Errorf("%w: token expired", gantry.ErrToolAuth)
	reg.Add(failingTool{name: "boom", err: authErr})
	reg.Add(addOneTool{})
	for i, occ := range occupiers {
		reg.Add(namedTool{Tool: occ, name: fmt.Sprintf("occupier%d", i)})
	}
	if err := a.With(tool.NewWithPolicy(reg, tool.Policy{Parallelism: numHeld + 1})); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	live := map[string]gantry.Event{}
	if _, err := a.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		if ev.Type == gantry.EventToolResultLive && ev.ToolResult != nil {
			live[ev.ToolResult.CallID] = ev
		}
		return nil
	}); err == nil || !errors.Is(err, gantry.ErrToolPolicyAborted) {
		t.Fatalf("RunStream err = %v, want ErrToolPolicyAborted", err)
	}

	if len(live) != len(calls) {
		t.Fatalf("got %d live events, want %d (one per pending call, none missing)", len(live), len(calls))
	}
	for _, call := range calls {
		ev, ok := live[call.ID]
		if !ok {
			t.Errorf("no live event for call %q", call.ID)
			continue
		}
		if strings.HasPrefix(call.ID, "ok") {
			if !ev.ToolResult.IsError || ev.ToolResult.Content != gantry.ErrToolSkipped.Error() {
				t.Errorf("abandoned call %q live result = %+v, want the ErrToolSkipped placeholder", call.ID, ev.ToolResult)
			}
		}
		if ev.Timestamp.IsZero() {
			t.Errorf("live event for %q has zero Timestamp", call.ID)
		}
	}
}

func TestFromToolsWithPolicyDispatchesPendingCalls(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "add_one", Input: json.RawMessage(`5`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.FromToolsWithPolicy(tool.Policy{Parallelism: 1}, addOneTool{})); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.FinalOutput != "final" {
		t.Errorf("FinalOutput = %q", state.FinalOutput)
	}
}

package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

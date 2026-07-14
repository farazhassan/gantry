package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/eval"
)

// depthProbe is a test tool that records the sub-agent depth it observes,
// proving the ctx-carried counter flows through a child run into that child's
// own tool invocations.
type depthProbe struct {
	mu   sync.Mutex
	seen []int
}

func (p *depthProbe) Definition() gantry.ToolDef {
	return gantry.ToolDef{
		Name:        "probe",
		Description: "records the sub-agent depth",
		Schema:      json.RawMessage(`{"type":"object","properties":{}}`),
	}
}

func (p *depthProbe) Invoke(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	p.mu.Lock()
	p.seen = append(p.seen, depthFrom(ctx))
	p.mu.Unlock()
	return json.RawMessage(`"ok"`), nil
}

// blockingLLM blocks until ctx is cancelled — a stand-in for a hung provider.
type blockingLLM struct{}

func (blockingLLM) Generate(ctx context.Context, _ gantry.LLMRequest) (gantry.LLMResponse, error) {
	<-ctx.Done()
	return gantry.LLMResponse{}, ctx.Err()
}

func TestChildRunsAtIncrementedDepth(t *testing.T) {
	probe := &depthProbe{}
	childLLM := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "p1", Name: "probe", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	child, err := gantry.NewAgent(
		gantry.WithLLM(childLLM),
		gantry.WithComponents(tool.FromTools(1, probe)),
	)
	if err != nil {
		t.Fatalf("NewAgent(child): %v", err)
	}
	tl := New("specialist", "runs the probe", child)

	if _, err := tl.Invoke(context.Background(), json.RawMessage(`{"goal":"probe"}`)); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if len(probe.seen) != 1 || probe.seen[0] != 1 {
		t.Errorf("probe.seen = %v, want [1] (child runs one level deeper)", probe.seen)
	}
}

func TestInvokeAtDefaultMaxDepthIsToolError(t *testing.T) {
	child := newChildAgent(t) // empty script: the child must never run
	tl := New("specialist", "d", child)

	_, err := tl.Invoke(withDepth(context.Background(), 3), json.RawMessage(`{"goal":"g"}`))
	if err == nil {
		t.Fatalf("Invoke at depth 3 = nil error, want depth-limit error")
	}
	if !strings.Contains(err.Error(), "depth") {
		t.Errorf("error = %q, want it to mention the depth limit", err)
	}
}

func TestWithMaxDepthLowersTheCap(t *testing.T) {
	child := newChildAgent(t)
	tl := New("specialist", "d", child, WithMaxDepth(1))

	if _, err := tl.Invoke(withDepth(context.Background(), 1), json.RawMessage(`{"goal":"g"}`)); err == nil {
		t.Errorf("Invoke at depth 1 with cap 1 = nil error, want depth-limit error")
	}
}

func TestDepthBelowCapStillRuns(t *testing.T) {
	child := newChildAgent(t, gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	tl := New("specialist", "d", child)

	if _, err := tl.Invoke(withDepth(context.Background(), 2), json.RawMessage(`{"goal":"g"}`)); err != nil {
		t.Errorf("Invoke at depth 2 (cap 3): %v", err)
	}
}

func TestWithTimeoutCancelsChildRun(t *testing.T) {
	child, err := gantry.NewAgent(gantry.WithLLM(blockingLLM{}))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	tl := New("slow_specialist", "d", child, WithTimeout(30*time.Millisecond))

	_, err = tl.Invoke(context.Background(), json.RawMessage(`{"goal":"g"}`))
	if err == nil {
		t.Fatalf("Invoke = nil error, want a deadline error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want errors.Is(err, context.DeadlineExceeded)", err)
	}
}

func TestChildEventsScopedOutByDefault(t *testing.T) {
	child := newChildAgent(t, gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	tl := New("specialist", "d", child)

	var events int
	ctx := gantry.WithSink(context.Background(), func(gantry.Event) error {
		events++
		return nil
	})
	if _, err := tl.Invoke(ctx, json.RawMessage(`{"goal":"g"}`)); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if events != 0 {
		t.Errorf("ambient sink saw %d child events, want 0 (scoped out by default)", events)
	}
}

func TestWithEventPassthroughKeepsAmbientSink(t *testing.T) {
	child := newChildAgent(t, gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	tl := New("specialist", "d", child, WithEventPassthrough())

	var events int
	ctx := gantry.WithSink(context.Background(), func(gantry.Event) error {
		events++
		return nil
	})
	if _, err := tl.Invoke(ctx, json.RawMessage(`{"goal":"g"}`)); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if events == 0 {
		t.Errorf("ambient sink saw no child events, want phase/done events with passthrough")
	}
}

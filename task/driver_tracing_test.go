package task

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
)

type fakeSpan struct {
	name   string
	attrs  map[string]any
	ended  bool
	endErr error
}

func (s *fakeSpan) SetAttr(k string, v any)                { s.attrs[k] = v }
func (s *fakeSpan) RecordEvent(_ string, _ map[string]any) {}
func (s *fakeSpan) End(err error)                          { s.ended = true; s.endErr = err }

type fakeSpanKey struct{}

type fakeTracer struct{ spans []*fakeSpan }

func (tr *fakeTracer) StartSpan(ctx context.Context, name string) (context.Context, gantry.Span) {
	s := &fakeSpan{name: name, attrs: map[string]any{}}
	tr.spans = append(tr.spans, s)
	return context.WithValue(ctx, fakeSpanKey{}, s), s
}

type ctxProbeRunner struct{ sawSpan bool }

func (r *ctxProbeRunner) Resume(ctx context.Context, st *gantry.State) (*gantry.State, error) {
	if _, ok := ctx.Value(fakeSpanKey{}).(*fakeSpan); ok {
		r.sawSpan = true
	}
	st.Done = true
	st.DoneReason = gantry.DoneNoToolCalls
	return st, nil
}

func TestAdvanceEmitsTaskSpan(t *testing.T) {
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{done(gantry.DoneNoToolCalls, nil)}}
	tr := &fakeTracer{}
	d := NewDriver(runner, NewInMemory(), WithTracer(tr))
	tk := &Task{ID: "tk-1", SessionID: "s1", Title: "T", Status: TaskPending}

	if _, err := d.Advance(context.Background(), tk, "do it"); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if len(tr.spans) != 1 {
		t.Fatalf("got %d spans, want exactly 1", len(tr.spans))
	}
	sp := tr.spans[0]
	if sp.name != "task" {
		t.Errorf("span name = %q, want task", sp.name)
	}
	if sp.attrs["task.id"] != "tk-1" || sp.attrs["session.id"] != "s1" || sp.attrs["task.title"] != "T" {
		t.Errorf("start attrs = %+v", sp.attrs)
	}
	if !sp.ended || sp.endErr != nil {
		t.Errorf("ended=%v endErr=%v, want ended with nil", sp.ended, sp.endErr)
	}
	if sp.attrs["task.status"] != string(TaskDone) {
		t.Errorf("task.status = %v, want done", sp.attrs["task.status"])
	}
	if runs, _ := sp.attrs["task.runs"].(int); runs != 1 {
		t.Errorf("task.runs = %v, want 1", sp.attrs["task.runs"])
	}
}

func TestAdvanceNilTracerNoSpanNoPanic(t *testing.T) {
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{done(gantry.DoneNoToolCalls, nil)}}
	d := NewDriver(runner, NewInMemory())
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskDone {
		t.Errorf("status = %q, want done (nil tracer must not change behavior)", got.Status)
	}
}

func TestAdvanceMultiRunSingleSpan(t *testing.T) {
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneMaxIterations, twoStepPlan()),
		done(gantry.DoneNoToolCalls, nil),
	}}
	tr := &fakeTracer{}
	d := NewDriver(runner, NewInMemory(), WithTracer(tr))
	tk := &Task{ID: "tk-1", Status: TaskPending}

	if _, err := d.Advance(context.Background(), tk, "do it"); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if len(tr.spans) != 1 {
		t.Fatalf("got %d spans, want exactly 1 for a multi-run drive-cycle", len(tr.spans))
	}
	if runs, _ := tr.spans[0].attrs["task.runs"].(int); runs != 2 {
		t.Errorf("task.runs = %v, want 2", tr.spans[0].attrs["task.runs"])
	}
}

func TestAdvanceSpanNestsViaCtx(t *testing.T) {
	r := &ctxProbeRunner{}
	tr := &fakeTracer{}
	d := NewDriver(r, NewInMemory(), WithTracer(tr))
	tk := &Task{ID: "tk-1", Status: TaskPending}

	if _, err := d.Advance(context.Background(), tk, "do it"); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if !r.sawSpan {
		t.Error("runner did not receive the task-span ctx; runs would not nest under the task span")
	}
}

func TestAdvanceErrorEndsSpanWithError(t *testing.T) {
	sentinel := errors.New("boom")
	runner := &scriptedRunner{
		steps: []func(*gantry.State) *gantry.State{done(gantry.DoneNoToolCalls, nil)},
		err:   sentinel,
		errOn: 0,
	}
	tr := &fakeTracer{}
	d := NewDriver(runner, NewInMemory(), WithTracer(tr))
	tk := &Task{ID: "tk-1", Status: TaskPending}

	if _, err := d.Advance(context.Background(), tk, "do it"); !errors.Is(err, sentinel) {
		t.Fatalf("Advance err = %v, want wrapped sentinel", err)
	}
	sp := tr.spans[0]
	if !sp.ended || !errors.Is(sp.endErr, sentinel) {
		t.Errorf("span endErr = %v, want wrapped sentinel", sp.endErr)
	}
	if sp.attrs["task.status"] != string(TaskFailed) {
		t.Errorf("task.status = %v, want failed", sp.attrs["task.status"])
	}
}

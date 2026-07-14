package task

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
)

// registryResolver returns a resolver backed by a map keyed on AgentProfile —
// the registry shape a taskmanager integrator wires in production. Missing keys
// resolve to nil, which Advance treats as "fall back to the constructor Runner".
func registryResolver(reg map[string]Runner) func(*Task) Runner {
	return func(t *Task) Runner { return reg[t.AgentProfile] }
}

func TestAdvanceResolvesRunnerByProfile(t *testing.T) {
	def := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneNoToolCalls, nil),
	}}
	prof := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneNoToolCalls, nil),
	}}
	d := NewDriver(def, NewInMemory(), WithRunnerResolver(registryResolver(map[string]Runner{
		"researcher": prof,
	})))
	tk := &Task{ID: "tk-1", Status: TaskPending, AgentProfile: "researcher"}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskDone {
		t.Errorf("status = %q, want done", got.Status)
	}
	if prof.calls != 1 {
		t.Errorf("profile runner calls = %d, want 1", prof.calls)
	}
	if def.calls != 0 {
		t.Errorf("constructor runner calls = %d, want 0 (resolved away)", def.calls)
	}
}

func TestAdvanceUnknownProfileFallsBack(t *testing.T) {
	def := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneNoToolCalls, nil),
	}}
	prof := &scriptedRunner{}
	d := NewDriver(def, NewInMemory(), WithRunnerResolver(registryResolver(map[string]Runner{
		"researcher": prof,
	})))
	tk := &Task{ID: "tk-1", Status: TaskPending, AgentProfile: "no-such-profile"}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskDone {
		t.Errorf("status = %q, want done", got.Status)
	}
	if def.calls != 1 {
		t.Errorf("constructor runner calls = %d, want 1 (fallback)", def.calls)
	}
	if prof.calls != 0 {
		t.Errorf("profile runner calls = %d, want 0", prof.calls)
	}
}

func TestAdvanceEmptyProfileFallsBack(t *testing.T) {
	def := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneNoToolCalls, nil),
	}}
	d := NewDriver(def, NewInMemory(), WithRunnerResolver(registryResolver(map[string]Runner{})))
	tk := &Task{ID: "tk-1", Status: TaskPending} // AgentProfile ""

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskDone {
		t.Errorf("status = %q, want done", got.Status)
	}
	if def.calls != 1 {
		t.Errorf("constructor runner calls = %d, want 1 (empty profile falls back)", def.calls)
	}
}

func TestAdvanceNilResolverUsesConstructorRunner(t *testing.T) {
	def := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneNoToolCalls, nil),
	}}
	d := NewDriver(def, NewInMemory()) // no resolver option
	tk := &Task{ID: "tk-1", Status: TaskPending, AgentProfile: "researcher"}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskDone {
		t.Errorf("status = %q, want done", got.Status)
	}
	if def.calls != 1 {
		t.Errorf("constructor runner calls = %d, want 1 (nil resolver)", def.calls)
	}
}

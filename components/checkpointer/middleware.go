package checkpointer

import (
	"context"
	"errors"
	"fmt"

	"github.com/farazhassan/gantry"
)

type component struct {
	c           Checkpointer
	id          string
	extraPhases []gantry.Phase
}

// New returns a Component that wires a Checkpointer (keyed by id) into the
// agent. It installs PhaseEnd middleware that calls Save unconditionally —
// by the time PhaseEnd runs, the loop has always terminated cleanly. Save
// errors there are wrapped as ErrCheckpointFailed and recorded on
// state.Trace but do not abort the run.
//
// extraPhases registers an additional save point on each named phase, for
// mid-run checkpointing: PhaseEnd alone cannot recover a crash mid-iteration,
// and — more subtly — PhaseEnd is never reached at all when an earlier
// phase aborts the run with an error (e.g. components/humanloop rejecting a
// tool call sets state.Done and returns ErrHumanAborted without calling
// next). Unlike the PhaseEnd hook, an extraPhases hook saves state even
// when a later middleware on the same phase returns an error, so an
// aborted run is still captured.
//
// For that to work, New must be installed via Agent.With AFTER any
// middleware on the same phase that can itself abort the run (e.g. after
// humanloop.New) — Compose (middleware.go) makes the last-registered
// middleware on a phase the outermost one, and a middleware only observes
// an error from another middleware registered before it (further in) via
// its own call to next; one registered after it (further out) never sees
// that error at all.
func New(c Checkpointer, id string, extraPhases ...gantry.Phase) gantry.Component {
	return &component{c: c, id: id, extraPhases: extraPhases}
}

func (comp *component) Install(a *gantry.Agent) error {
	const endName = "components/checkpointer:save"
	if err := a.UseNamed(gantry.PhaseEnd, endName, comp.saveOnSuccess); err != nil {
		return err
	}
	for _, phase := range comp.extraPhases {
		if phase == gantry.PhaseEnd {
			return errors.New("checkpointer: PhaseEnd is not a valid extraPhases entry; PhaseEnd is already saved unconditionally by New")
		}
		name := "components/checkpointer:save:" + string(phase)
		if err := a.UseNamed(phase, name, comp.saveAlways); err != nil {
			return err
		}
	}
	return nil
}

// saveOnSuccess is PhaseEnd's hook: save only if the wrapped chain succeeds.
func (comp *component) saveOnSuccess(next gantry.Handler) gantry.Handler {
	return func(ctx context.Context, s *gantry.State) error {
		if err := next(ctx, s); err != nil {
			return err
		}
		comp.save(ctx, s)
		return nil
	}
}

// saveAlways is an extraPhases hook: save regardless of whether the wrapped
// chain errors, so a mid-phase abort is still checkpointed. The original
// error (or nil) is always propagated unchanged.
func (comp *component) saveAlways(next gantry.Handler) gantry.Handler {
	return func(ctx context.Context, s *gantry.State) error {
		err := next(ctx, s)
		comp.save(ctx, s)
		return err
	}
}

func (comp *component) save(ctx context.Context, s *gantry.State) {
	if err := comp.c.Save(ctx, comp.id, s); err != nil {
		wrapped := fmt.Errorf("%w: %v", gantry.ErrCheckpointFailed, err)
		if s.Trace != nil {
			s.Trace.Record(gantry.TraceEvent{
				Name:  "checkpoint_failed",
				Kind:  gantry.KindEvent,
				Err:   wrapped,
				Attrs: map[string]any{"id": comp.id},
			})
		}
		// Non-fatal per spec.
	}
}

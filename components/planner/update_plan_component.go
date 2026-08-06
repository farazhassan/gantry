package planner

import (
	"context"
	"errors"

	"github.com/farazhassan/gantry"
)

// Middleware names installed by UpdatePlan.
const (
	updatePlanAdvertiseName = "components/planner:update_plan_advertise"
	updatePlanExecName      = "components/planner:update_plan_exec"
)

type updatePlanComponent struct{}

// UpdatePlan returns a Component that lets the model maintain the run's plan
// through an intercepted "update_plan" pseudo-tool, mirroring how
// components/tool's Client keeps client-side calls away from generic dispatch.
// The call never reaches a tool Registry: a tool.Tool cannot implement it,
// because Invoke receives only ctx and input, never the *State. It installs:
//
//   - PhaseAssembleContext "components/planner:update_plan_advertise" —
//     appends UpdatePlanToolDef() to state.Tools whenever the run has a plan.
//     state.Tools is reset at the top of every run and rebuilt by PhaseStart
//     middleware; this phase re-runs every iteration, so the append dedupes by
//     name. Running here (not PhaseStart) also catches plans born in PhasePlan
//     during iteration 0.
//   - PhaseToolExec "components/planner:update_plan_exec" — intercepts pending
//     calls named "update_plan", applies their ops directly to state.Plan, and
//     synthesizes the tool result; DefaultObserveHandler folds it into the
//     transcript like any other result.
//
// Ordering: middleware registration is innermost-first (gantry.Compose), so
// the LAST-registered PhaseToolExec middleware runs FIRST. Install UpdatePlan
// AFTER the tool-dispatch component and the interception runs before generic
// dispatch. The reverse order still behaves correctly — dispatch records an
// "unknown tool" error result first and the interception, running after,
// replaces it via upsertResult — it just wastes a registry miss.
//
// Installing UpdatePlan twice on the same agent returns an error. Do not also
// register a real tool named "update_plan": dispatch would race the
// interception for the same call.
func UpdatePlan() gantry.Component { return updatePlanComponent{} }

func (updatePlanComponent) Install(a *gantry.Agent) error {
	for _, name := range a.MiddlewareNames(gantry.PhaseToolExec) {
		if name == updatePlanExecName {
			return errors.New("planner: update_plan already installed on this agent")
		}
	}

	if err := a.UseNamed(gantry.PhaseAssembleContext, updatePlanAdvertiseName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if s.Plan != nil && !hasToolDef(s.Tools, UpdatePlanToolName) {
				s.Tools = append(s.Tools, UpdatePlanToolDef())
			}
			return next(ctx, s)
		}
	}); err != nil {
		return err
	}

	return a.UseNamed(gantry.PhaseToolExec, updatePlanExecName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			intercepted := false
			for _, call := range s.PendingToolCalls {
				if call.Name == UpdatePlanToolName {
					intercepted = true
					break
				}
			}
			if !intercepted {
				return next(ctx, s)
			}
			var rest []gantry.ToolCall
			for _, call := range s.PendingToolCalls {
				if call.Name != UpdatePlanToolName {
					rest = append(rest, call)
					continue
				}
				content, isErr, results := runUpdatePlan(s.Plan, call.Input)
				upsertResult(s, gantry.ToolResult{CallID: call.ID, Content: content, IsError: isErr})
				if err := emitPlanStepChanges(ctx, s, results); err != nil {
					return err
				}
			}
			// Removing the intercepted calls keeps generic dispatch (and the
			// client-tool suspend middleware in PhaseObserve) from ever seeing
			// them.
			s.PendingToolCalls = rest
			return next(ctx, s)
		}
	})
}

// hasToolDef reports whether defs already contains a tool named name.
func hasToolDef(defs []gantry.ToolDef, name string) bool {
	for _, d := range defs {
		if d.Name == name {
			return true
		}
	}
	return false
}

// upsertResult records a synthesized result, replacing any existing result for
// the same call id. The replace path makes interception order-independent:
// when generic dispatch ran first (UpdatePlan installed before the tool
// component), it has already recorded an "unknown tool" error result for the
// call — overwriting it instead of appending avoids duplicate tool results for
// one call id.
func upsertResult(s *gantry.State, res gantry.ToolResult) {
	for i := range s.ToolResults {
		if s.ToolResults[i].CallID == res.CallID {
			s.ToolResults[i] = res
			return
		}
	}
	s.ToolResults = append(s.ToolResults, res)
}

// emitPlanStepChanges emits a gantry.EventPlanStepChanged for every op in
// results that successfully targeted a step (set_status, set_output, and
// add_step all report StepID on success — see opResult), carrying that
// step's current full state so an AG-UI client can render live plan
// progress (see components/ui/agui's Mapper.activityChange). A no-op when
// no sink is active (gantry.Emit already guards that) or the plan is nil.
func emitPlanStepChanges(ctx context.Context, s *gantry.State, results []opResult) error {
	if s.Plan == nil {
		return nil
	}
	for _, r := range results {
		if !r.OK || r.StepID == "" {
			continue
		}
		step := findStep(s.Plan, r.StepID)
		if step == nil {
			continue
		}
		// Copy by value: step aliases live plan state (findStep returns
		// &plan.Steps[i]), and a buffered/async sink may not read PlanStep
		// until after a later update_plan call has mutated it further.
		snapshot := *step
		if err := gantry.Emit(ctx, gantry.Event{
			Type:      gantry.EventPlanStepChanged,
			Iteration: s.Iteration,
			PlanStep:  &snapshot,
		}); err != nil {
			return err
		}
	}
	return nil
}

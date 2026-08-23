// components/subagent/resume.go
package subagent

import (
	"context"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
)

// Resume fulfills pending calls on a state that was suspended by an agent
// wired with Component/ComponentWithRegistry — the same generic mechanism as
// tool.Resume, but with delegate usage-accounting wired up so a resumed
// delegate's child usage actually reaches the parent's state.Usage.
//
// Why this exists: tool.Resume is components/tool's generic, subagent-unaware
// entry point — it works for any ResumableTool and knows nothing about how
// subagent tracks delegate usage. During a LIVE Agent.Run/Agent.Resume pass,
// Component's PhaseToolExec middleware installs a *usageRecorder into ctx
// around tool dispatch and folds its total into state.Usage when the pass
// returns; delegateTool.Invoke/Resume look up that recorder via ctx and add
// their child's usage to it. But an application's call to tool.Resume to
// fulfill a suspended run happens OUTSIDE that pipeline — it's ordinary
// caller code invoking a package function, not a phase the middleware wraps
// — so the ctx it passes down to a nested delegateTool.Resume never has a
// recorder installed, usageRecorderFrom finds nothing, and the resumed
// delegate's own resume-leg usage is silently dropped instead of being
// folded into the returned state.
//
// Resume closes that gap: it installs a fresh recorder into ctx itself
// (mirroring what Component's middleware does for a live pass), delegates to
// tool.Resume, and folds the recorder's total into the state tool.Resume
// returns before handing it back — including on error, since a partially
// completed resume may still have consumed real, billable tokens that must
// not be dropped just because the round did not finish cleanly.
//
// This composes across nested delegates without double-counting: a
// delegateTool.Resume invoked at any depth (directly here, or from inside
// another delegateTool.Resume issuing its own nested tool.Resume call) finds
// the SAME recorder via ctx, because withDepth/gantry.WithoutSink/
// context.WithTimeout all wrap ctx with the standard context.WithValue/
// WithCancel/WithTimeout chaining, which preserves ancestor values rather
// than stripping them. Each level folds only its own local delta (the
// child's cumulative usage minus its usage as of just before this round —
// see delegateTool.Resume) into that one shared recorder, so summing every
// level's disjoint delta and folding once here, after tool.Resume returns,
// is correct regardless of how many levels of nesting the pending calls
// span.
//
// Use Resume instead of calling tool.Resume directly whenever the suspended
// state may involve a subagent delegate and usage accounting matters to the
// caller. A bare tool.Resume call remains fully valid — e.g. for a state
// whose pending calls never touch a delegate tool, where this fold is a
// no-op anyway — Resume is an additive convenience on top of it, not a
// replacement.
func Resume(ctx context.Context, agent *gantry.Agent, reg *tool.Registry, state *gantry.State, answers []gantry.ToolResult) (*gantry.State, error) {
	rec := &usageRecorder{}
	st, err := tool.Resume(withUsageRecorder(ctx, rec), agent, reg, state, answers)
	// Fold even on error: tool.Resume always returns a non-nil, progress-
	// reflecting state, and an errored resume attempt may still have run (and
	// paid for) some nested delegate work before failing.
	if st != nil {
		st.Usage = st.Usage.Add(rec.total())
	}
	return st, err
}

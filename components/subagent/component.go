package subagent

import (
	"context"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
)

// usageFoldName is the PhaseToolExec middleware installed by Component.
const usageFoldName = "components/subagent:usage_fold"

type component struct {
	parallelism int
	tools       []tool.Tool
}

// Component wires tools into an agent like tool.FromTools AND installs a
// PhaseToolExec middleware that folds child-run usage recorded by delegate
// tools into the parent run's State.Usage. Pass ALL the agent's tools here
// (delegates and plain tools alike): tool dispatch may only be installed once
// per agent, so on agents that use delegates Component replaces — not
// supplements — tool.FromTools.
//
// How the fold works: gantry.Compose treats registration order as
// innermost-first, so the fold middleware — registered by this Install AFTER
// dispatch — wraps it. On each PhaseToolExec pass the fold injects a fresh
// *usageRecorder into ctx, delegates to dispatch (whose ctx flows into every
// Tool.Invoke, where delegate tools add their child state's Usage), then adds
// the recorder's total into state.Usage. Tool.Invoke cannot reach the parent
// *State directly; this ctx seam is the bridge.
func Component(parallelism int, tools ...tool.Tool) gantry.Component {
	return &component{parallelism: parallelism, tools: tools}
}

func (c *component) Install(a *gantry.Agent) error {
	if err := a.With(tool.FromTools(c.parallelism, c.tools...)); err != nil {
		return err
	}
	return a.UseNamed(gantry.PhaseToolExec, usageFoldName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			rec := &usageRecorder{}
			err := next(withUsageRecorder(ctx, rec), s)
			// Fold even on error: an aborted dispatch may still have completed
			// (and paid for) some child runs.
			s.Usage = s.Usage.Add(rec.total())
			return err
		}
	})
}

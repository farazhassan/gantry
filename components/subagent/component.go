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
	reg         *tool.Registry
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
	c, _ := newComponent(parallelism, tools)
	return c
}

// ComponentWithRegistry is Component, additionally returning the
// *tool.Registry it builds. Needed by tool.Resume (to look up which
// ResumableTool owns a pending call) and by a parent delegate tool's own
// WithChildRegistry when this agent is itself wired as someone else's child
// (more than one level of nesting). Component itself is unchanged and
// remains the right choice for callers that never resume a suspended run.
func ComponentWithRegistry(parallelism int, tools ...tool.Tool) (gantry.Component, *tool.Registry) {
	return newComponent(parallelism, tools)
}

func newComponent(parallelism int, tools []tool.Tool) (*component, *tool.Registry) {
	reg := tool.NewRegistry()
	for _, t := range tools {
		reg.Add(t)
	}
	c := &component{parallelism: parallelism, tools: tools, reg: reg}
	return c, reg
}

func (c *component) Install(a *gantry.Agent) error {
	if err := a.With(tool.New(c.reg, c.parallelism)); err != nil {
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

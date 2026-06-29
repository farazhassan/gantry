package tool

import (
	"context"
	"errors"

	"github.com/farazhassan/gantry"
)

// Middleware names installed by New (and therefore by FromTools, which delegates
// to it).
const (
	registerDefsName = "components/tool:register_defs"
	dispatchName     = "components/tool:dispatch"
)

type registryComponent struct {
	reg         *Registry
	parallelism int
}

// New returns a Component wiring a caller-owned Registry into the agent. It
// installs PhaseStart "components/tool:register_defs" (appends reg.Definitions()
// to state.Tools) and PhaseToolExec "components/tool:dispatch" (dispatches pending
// tool calls against reg with up to parallelism concurrent invocations;
// parallelism <= 0 means full parallelism). Installing tool dispatch twice on the
// same agent returns an error.
func New(reg *Registry, parallelism int) gantry.Component {
	return &registryComponent{reg: reg, parallelism: parallelism}
}

// FromTools returns a Component that builds a Registry from the given tools and
// wires it in with parallel dispatch up to parallelism simultaneous calls. It is
// sugar over New for callers that do not need to retain the Registry. For a single
// tool with sequential dispatch, use FromTools(1, t).
func FromTools(parallelism int, tools ...Tool) gantry.Component {
	reg := NewRegistry()
	for _, t := range tools {
		reg.Add(t)
	}
	return &registryComponent{reg: reg, parallelism: parallelism}
}

func (c *registryComponent) Install(a *gantry.Agent) error {
	for _, name := range a.MiddlewareNames(gantry.PhaseToolExec) {
		if name == dispatchName {
			return errors.New("tool: dispatch middleware already installed on this agent (" + dispatchName + ")")
		}
	}

	if err := a.UseNamed(gantry.PhaseStart, registerDefsName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			s.Tools = append(s.Tools, c.reg.Definitions()...)
			return next(ctx, s)
		}
	}); err != nil {
		return err
	}

	return a.UseNamed(gantry.PhaseToolExec, dispatchName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			set := clientToolSet(s)
			// A client-tool name that also names a registered tool is a wiring
			// bug: dispatch would skip the executable tool. Catch it loudly.
			for name := range set {
				if _, ok := c.reg.Lookup(name); ok {
					panic("tool: client tool name collides with a registered tool: " + name)
				}
			}
			// Dispatch only server-side (non-client) calls; client-side calls
			// stay in s.PendingToolCalls for the suspend middleware.
			var calls []gantry.ToolCall
			for _, cl := range s.PendingToolCalls {
				if !set[cl.Name] {
					calls = append(calls, cl)
				}
			}
			if len(calls) == 0 {
				return next(ctx, s)
			}
			results := make([]gantry.ToolResult, len(calls))
			jobs := make([]func(ctx context.Context) error, len(calls))
			for i, call := range calls {
				i, call := i, call
				jobs[i] = func(ctx context.Context) error {
					out, err := c.reg.Invoke(ctx, call)
					if err != nil {
						results[i] = gantry.ToolResult{
							CallID:  call.ID,
							Content: err.Error(),
							IsError: true,
							Err:     err,
						}
						// Tool failures are recorded and surfaced to the LLM as
						// error results; they do not abort the run. Non-tool
						// errors are also preserved for middleware introspection.
						if !errors.Is(err, gantry.ErrToolExecution) {
							results[i].Err = err
						}
						return nil
					}
					results[i] = gantry.ToolResult{
						CallID:  call.ID,
						Content: string(out),
					}
					return nil
				}
			}
			if err := gantry.RunParallel(ctx, c.parallelism, jobs); err != nil {
				return err
			}
			s.ToolResults = append(s.ToolResults, results...)
			return next(ctx, s)
		}
	})
}

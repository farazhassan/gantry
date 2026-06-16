package tool

import (
	"context"
	"errors"

	"github.com/farazhassan/gantry"
)

// Middleware names installed by WithRegistry (and therefore by WithTools and
// WithTool, which delegate to it).
const (
	registerDefsName = "components/tool:register_defs"
	dispatchName     = "components/tool:dispatch"
)

// WithRegistry wires a caller-owned Registry into the agent. The caller owns
// the registry's lifetime: tool state lives exactly as long as the caller
// retains reg (or as long as the agent, when reg is not retained elsewhere).
// Dropping the agent frees its tools — there is no process-global retention
// and no manual cleanup.
//
// A single Registry may be shared across multiple agents, and the caller may
// mutate it at runtime via reg.Add: the dispatch closure holds the pointer and
// the register_defs middleware re-reads reg.Definitions() at the start of each
// Run, so tools added between runs become visible on the next Run.
//
// It installs two middleware:
//
//   - PhaseStart "components/tool:register_defs" appends reg.Definitions() to
//     state.Tools.
//   - PhaseToolExec "components/tool:dispatch" dispatches state.PendingToolCalls
//     against reg with up to `parallelism` concurrent invocations
//     (parallelism <= 0 means full parallelism).
//
// Calling WithRegistry (or WithTools/WithTool) more than once on the same agent
// is a wiring bug and panics: the dispatch middleware can only be installed
// once per agent (precedent: net/http's ServeMux.Handle).
func WithRegistry(a *gantry.Agent, reg *Registry, parallelism int) {
	for _, name := range a.MiddlewareNames(gantry.PhaseToolExec) {
		if name == dispatchName {
			panic("tool: WithRegistry called more than once on the same agent (" + dispatchName + " already installed)")
		}
	}

	_ = a.UseNamed(gantry.PhaseStart, registerDefsName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			s.Tools = append(s.Tools, reg.Definitions()...)
			return next(ctx, s)
		}
	})

	_ = a.UseNamed(gantry.PhaseToolExec, dispatchName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			calls := s.PendingToolCalls
			if len(calls) == 0 {
				return next(ctx, s)
			}
			results := make([]gantry.ToolResult, len(calls))
			jobs := make([]func(ctx context.Context) error, len(calls))
			for i, call := range calls {
				i, call := i, call
				jobs[i] = func(ctx context.Context) error {
					out, err := reg.Invoke(ctx, call)
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
			if err := gantry.RunParallel(ctx, parallelism, jobs); err != nil {
				return err
			}
			s.ToolResults = append(s.ToolResults, results...)
			return next(ctx, s)
		}
	})
}

// WithTools builds a Registry containing the given tools and wires it into the
// agent with parallel dispatch up to `parallelism` simultaneous tool calls
// during PhaseToolExec (parallelism <= 0 means full parallelism). It is sugar
// over WithRegistry for callers that do not need to retain the Registry.
func WithTools(a *gantry.Agent, parallelism int, tools ...Tool) {
	reg := NewRegistry()
	for _, t := range tools {
		reg.Add(t)
	}
	WithRegistry(a, reg, parallelism)
}

// WithTool registers a single tool with sequential dispatch (one tool call at
// a time). It is sugar over WithTools(a, 1, t).
func WithTool(a *gantry.Agent, t Tool) {
	WithTools(a, 1, t)
}

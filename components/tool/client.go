package tool

import (
	"context"

	"github.com/farazhassan/gantry"
)

// Middleware names installed by WithClientTools.
const (
	advertiseClientName = "components/tool:client_advertise"
	suspendName         = "components/tool:client_suspend"

	// clientToolsMetaKey carries the set of client-side tool names from the
	// PhaseStart advertise middleware to the PhaseToolExec dispatch and the
	// PhaseObserve suspend middleware. Namespaced per the State.Meta convention.
	clientToolsMetaKey = "components/tool:client_tools"
)

// WithClientTools declares definition-only "client-side" tools on an agent.
// Their ToolDefs are advertised to the LLM alongside any executable tools, but
// they have no server-side Invoke: when the model calls one, the dispatch
// middleware skips it and the run suspends at the observe boundary with
// state.Done == true and state.DoneReason == gantry.DoneClientToolCall, leaving
// the unfulfilled client call(s) in state.PendingToolCalls.
//
// To fulfill a suspended run the caller appends a tool-result Message for each
// pending call. Because the suspended state is terminal (Done == true), Resume
// and ResumeStream no-op on it as-is: the caller must first clear the terminal
// fields (set Done = false, DoneReason = "", PendingToolCalls = nil) before
// resuming, or build a fresh non-terminal State from the transcript (this is
// what the AG-UI handler does via input.ToResume).
//
// Mixed turns are handled cleanly: when one assistant message mixes server and
// client tool calls, the server calls execute and their results are recorded;
// the run suspends with only the client call(s) pending.
//
// Calling it more than once on the same agent is a wiring bug and panics. A
// client-tool name that collides with a registered executable tool is also a
// wiring bug and panics on the first tool dispatch (see WithRegistry).
func WithClientTools(a *gantry.Agent, defs ...gantry.ToolDef) {
	for _, name := range a.MiddlewareNames(gantry.PhaseStart) {
		if name == advertiseClientName {
			panic("tool: WithClientTools called more than once on the same agent")
		}
	}

	names := make(map[string]bool, len(defs))
	for _, d := range defs {
		if d.Name == "" {
			panic("tool: WithClientTools requires non-empty tool names")
		}
		if names[d.Name] {
			panic("tool: WithClientTools duplicate client tool name " + d.Name)
		}
		names[d.Name] = true
	}
	defsCopy := append([]gantry.ToolDef(nil), defs...)

	// PhaseStart: advertise client defs to the LLM and record the client-tool
	// name set so dispatch (PhaseToolExec) and suspend (PhaseObserve) can see it.
	_ = a.UseNamed(gantry.PhaseStart, advertiseClientName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			s.Tools = append(s.Tools, defsCopy...)
			if s.Meta == nil {
				s.Meta = map[string]any{}
			}
			s.Meta[clientToolsMetaKey] = names
			return next(ctx, s)
		}
	})

	// PhaseObserve: capture the client calls before DefaultObserveHandler clears
	// the pending slice, let server results persist, then restore the client
	// call(s) and suspend. Running suspend here (not in PhaseToolExec) is
	// required: the loop breaks on state.Done between phases, so setting Done in
	// tool_exec would skip observe and lose server results.
	_ = a.UseNamed(gantry.PhaseObserve, suspendName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			set := clientToolSet(s)
			var clientCalls []gantry.ToolCall
			for _, c := range s.PendingToolCalls {
				if set[c.Name] {
					clientCalls = append(clientCalls, c)
				}
			}
			if err := next(ctx, s); err != nil {
				return err
			}
			if len(clientCalls) > 0 {
				s.PendingToolCalls = append(s.PendingToolCalls[:0], clientCalls...)
				s.Done = true
				s.DoneReason = gantry.DoneClientToolCall
			}
			return nil
		}
	})
}

// clientToolSet returns the client-tool name set recorded by WithClientTools
// for this run, or nil when no client tools were configured. A nil map is safe
// to index (always reports false), so callers need no special-casing.
func clientToolSet(s *gantry.State) map[string]bool {
	if s.Meta == nil {
		return nil
	}
	set, _ := s.Meta[clientToolsMetaKey].(map[string]bool)
	return set
}

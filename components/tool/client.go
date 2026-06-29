package tool

import (
	"context"
	"errors"

	"github.com/farazhassan/gantry"
)

// Middleware names installed by Client.
const (
	advertiseClientName = "components/tool:client_advertise"
	suspendName         = "components/tool:client_suspend"

	// clientToolsMetaKey carries the set of client-side tool names from the
	// PhaseStart advertise middleware to the PhaseToolExec dispatch and the
	// PhaseObserve suspend middleware. Namespaced per the State.Meta convention.
	clientToolsMetaKey = "components/tool:client_tools"
)

type clientComponent struct{ defs []gantry.ToolDef }

// Client returns a Component declaring definition-only "client-side" tools. Their
// ToolDefs are advertised to the LLM, but they have no server-side Invoke: when the
// model calls one, the run suspends at the observe boundary with
// state.DoneReason == gantry.DoneClientToolCall and the call(s) left in
// state.PendingToolCalls. Installing client tools twice on the same agent, an empty
// tool name, or a duplicate name returns an error.
func Client(defs ...gantry.ToolDef) gantry.Component {
	return &clientComponent{defs: defs}
}

func (c *clientComponent) Install(a *gantry.Agent) error {
	for _, name := range a.MiddlewareNames(gantry.PhaseStart) {
		if name == advertiseClientName {
			return errors.New("tool: client tools already installed on this agent")
		}
	}

	names := make(map[string]bool, len(c.defs))
	for _, d := range c.defs {
		if d.Name == "" {
			return errors.New("tool: client tools require non-empty tool names")
		}
		if names[d.Name] {
			return errors.New("tool: duplicate client tool name " + d.Name)
		}
		names[d.Name] = true
	}
	defsCopy := append([]gantry.ToolDef(nil), c.defs...)

	// PhaseStart: advertise client defs to the LLM and record the client-tool
	// name set so dispatch (PhaseToolExec) and suspend (PhaseObserve) can see it.
	if err := a.UseNamed(gantry.PhaseStart, advertiseClientName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			s.Tools = append(s.Tools, defsCopy...)
			if s.Meta == nil {
				s.Meta = map[string]any{}
			}
			s.Meta[clientToolsMetaKey] = names
			return next(ctx, s)
		}
	}); err != nil {
		return err
	}

	// PhaseObserve: capture the client calls before DefaultObserveHandler clears
	// the pending slice, let server results persist, then restore the client
	// call(s) and suspend. Running suspend here (not in PhaseToolExec) is
	// required: the loop breaks on state.Done between phases, so setting Done in
	// tool_exec would skip observe and lose server results.
	return a.UseNamed(gantry.PhaseObserve, suspendName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			set := clientToolSet(s)
			var clientCalls []gantry.ToolCall
			for _, cl := range s.PendingToolCalls {
				if set[cl.Name] {
					clientCalls = append(clientCalls, cl)
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

// clientToolSet returns the client-tool name set recorded by Client
// for this run, or nil when no client tools were configured. A nil map is safe
// to index (always reports false), so callers need no special-casing.
func clientToolSet(s *gantry.State) map[string]bool {
	if s.Meta == nil {
		return nil
	}
	set, _ := s.Meta[clientToolsMetaKey].(map[string]bool)
	return set
}

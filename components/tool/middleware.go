package tool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/farazhassan/gantry"
)

// Middleware names installed by New (and therefore by FromTools, which delegates
// to it).
const (
	registerDefsName = "components/tool:register_defs"
	dispatchName     = "components/tool:dispatch"
)

type registryComponent struct {
	reg    *Registry
	policy Policy
}

// New returns a Component wiring a caller-owned Registry into the agent. It
// installs PhaseStart "components/tool:register_defs" (appends reg.Definitions()
// to state.Tools) and PhaseToolExec "components/tool:dispatch" (dispatches pending
// tool calls against reg with up to parallelism concurrent invocations;
// parallelism <= 0 means full parallelism). Installing tool dispatch twice on the
// same agent returns an error. Equivalent to
// NewWithPolicy(reg, Policy{Parallelism: parallelism}).
func New(reg *Registry, parallelism int) gantry.Component {
	return NewWithPolicy(reg, Policy{Parallelism: parallelism})
}

// NewWithPolicy is New with full control over batch-failure and
// run-disposition behavior. See Policy for the available options.
func NewWithPolicy(reg *Registry, policy Policy) gantry.Component {
	return &registryComponent{reg: reg, policy: policy}
}

// FromTools returns a Component that builds a Registry from the given tools and
// wires it in with parallel dispatch up to parallelism simultaneous calls. It is
// sugar over New for callers that do not need to retain the Registry. For a single
// tool with sequential dispatch, use FromTools(1, t). Equivalent to
// FromToolsWithPolicy(Policy{Parallelism: parallelism}, tools...).
func FromTools(parallelism int, tools ...Tool) gantry.Component {
	return FromToolsWithPolicy(Policy{Parallelism: parallelism}, tools...)
}

// FromToolsWithPolicy is FromTools with full control over batch-failure and
// run-disposition behavior. See Policy for the available options.
func FromToolsWithPolicy(policy Policy, tools ...Tool) gantry.Component {
	reg := NewRegistry()
	for _, t := range tools {
		reg.Add(t)
	}
	return &registryComponent{reg: reg, policy: policy}
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

			// execCtx/cancel let OnFailure=StopCancelInFlight signal running
			// calls without touching the caller's own ctx. aborted stops any
			// call that hasn't started yet (StopWaitInFlight and
			// StopCancelInFlight both set it); stopErr records the first
			// failure whose effective Disposition is HarnessStop,
			// independent of whatever gantry.RunParallel itself returns —
			// RunParallel's return value can also go non-nil purely because
			// our own cancel() fired, which must NOT be misread as a hard
			// stop when Disposition is FeedbackToLLM.
			execCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			var aborted atomic.Bool
			var stopMu sync.Mutex
			var stopErr error
			// emitMu serializes EventToolResultLive emissions across the
			// concurrently-executing job goroutines below, so the ambient
			// sink — which EventSink's contract says is "called
			// synchronously" — never receives two calls at once for this
			// event either, even though it now originates off the run
			// goroutine. See the EventSink doc comment.
			var emitMu sync.Mutex
			emitLive := func(ctx context.Context, result gantry.ToolResult) {
				emitMu.Lock()
				defer emitMu.Unlock()
				// Best-effort: a sink error here must not abort an
				// in-flight tool batch over a purely observational event.
				_ = gantry.Emit(ctx, gantry.Event{Type: gantry.EventToolResultLive, Iteration: s.Iteration, ToolResult: &result})
			}

			results := make([]gantry.ToolResult, len(calls))
			jobs := make([]func(ctx context.Context) error, len(calls))
			for i, call := range calls {
				i, call := i, call
				// Pre-fill with the same ErrToolSkipped placeholder the
				// aborted.Load() branch below produces. gantry.RunParallel
				// dispatches jobs one at a time and can abandon a queued job
				// (and everything after it) the instant execCtx is
				// cancelled, without ever invoking its closure — not even
				// the aborted.Load() check inside it. Without this, such a
				// job's results[i] would be left at Go's zero value
				// (empty CallID), silently corrupting the tool_use/
				// tool_result correspondence sent back to the LLM.
				results[i] = gantry.ToolResult{
					CallID:  call.ID,
					Content: gantry.ErrToolSkipped.Error(),
					IsError: true,
					Err:     gantry.ErrToolSkipped,
				}
				jobs[i] = func(ctx context.Context) error {
					if aborted.Load() {
						results[i] = gantry.ToolResult{
							CallID:  call.ID,
							Content: gantry.ErrToolSkipped.Error(),
							IsError: true,
							Err:     gantry.ErrToolSkipped,
						}
						emitLive(ctx, results[i])
						return nil
					}
					out, err := c.reg.Invoke(WithCallID(ctx, call.ID), call)
					if err != nil {
						results[i] = gantry.ToolResult{
							CallID:  call.ID,
							Content: err.Error(),
							IsError: true,
							Err:     err,
						}
						emitLive(ctx, results[i])
						onFailure, disposition := c.policy.OnFailure, c.policy.Disposition
						if errors.Is(err, gantry.ErrToolAuth) || errors.Is(err, gantry.ErrToolPersistent) {
							onFailure, disposition = StopCancelInFlight, HarnessStop
						}
						if onFailure != KeepGoing {
							aborted.Store(true)
							if onFailure == StopCancelInFlight {
								cancel()
							}
						}
						if disposition == HarnessStop {
							stopMu.Lock()
							if stopErr == nil {
								stopErr = err
							}
							stopMu.Unlock()
						}
						return nil
					}
					results[i] = gantry.ToolResult{
						CallID:  call.ID,
						Content: string(out),
					}
					emitLive(ctx, results[i])
					return nil
				}
			}

			runErr := gantry.RunParallel(execCtx, c.policy.Parallelism, jobs)
			s.ToolResults = append(s.ToolResults, results...)

			if stopErr != nil {
				s.Done = true
				s.DoneReason = gantry.DoneToolPolicyAborted
				return fmt.Errorf("%w: %w", gantry.ErrToolPolicyAborted, stopErr)
			}
			if runErr != nil && ctx.Err() != nil {
				// The caller's own context was cancelled (not our internal
				// execCtx cancel(), which only ever affects execCtx while
				// leaving ctx alone) — propagate as gantry always has.
				return runErr
			}
			return next(ctx, s)
		}
	})
}

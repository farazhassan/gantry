package gantry

import (
	"context"
	"errors"
)

// ContextOverflowHandler is called when PhaseLLMCall fails with an error
// matching ErrContextLengthExceeded. It may shrink state (typically
// state.Messages) and return retry == true to re-run the whole PhaseLLMCall
// middleware chain once — so guardrails, the limiter and any other LLM-call
// middleware see the shrunk input. Returning retry == false leaves the
// original error in place; a non-nil herr is joined with it.
type ContextOverflowHandler func(ctx context.Context, state *State, err error) (retry bool, herr error)

// OnContextOverflow registers the agent's ContextOverflowHandler. At most one
// may be registered (components/compactor installs one); a nil handler or a
// second registration is an error. The retry happens at most once per LLM
// call, inside the same phase span and phase events, which carry the
// "context_overflow.retry" span attribute when it ran.
func (a *Agent) OnContextOverflow(h ContextOverflowHandler) error {
	if h == nil {
		return errors.New("gantry: OnContextOverflow handler is nil")
	}
	if a.overflow != nil {
		return errors.New("gantry: OnContextOverflow handler already registered")
	}
	a.overflow = h
	return nil
}

// retryOnOverflow runs the context-overflow handler for an LLM-call error and,
// if it asks for a retry, re-runs handler once. It returns the error to report.
func (a *Agent) retryOnOverflow(ctx context.Context, state *State, span Span, handler Handler, err error) error {
	if a.overflow == nil || state.Done || !errors.Is(err, ErrContextLengthExceeded) {
		return err
	}
	retry, herr := a.overflow(ctx, state, err)
	if herr != nil {
		return errors.Join(err, herr)
	}
	if !retry {
		return err
	}
	span.SetAttr("context_overflow.retry", true)
	return handler(ctx, state)
}

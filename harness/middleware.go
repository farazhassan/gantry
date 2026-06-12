package harness

import "context"

// Handler is the per-phase action: it reads/mutates state, possibly calling
// out to the LLM client or tools.
type Handler func(ctx context.Context, state *State) error

// Middleware wraps a Handler. Returning a new Handler that calls next is
// the onion pattern: middleware controls when/whether to invoke the inner
// chain.
type Middleware func(next Handler) Handler

// noopHandler is used when no inner handler is registered for a phase.
func noopHandler(ctx context.Context, state *State) error { return nil }

// Compose wraps inner with middlewares so that registration order is
// innermost-first. Given mws = [A, B, C] and inner I, the result is
// C(B(A(I))). Execution flows from C downward into I and back out.
//
// If inner is nil, a no-op handler is used. If mws is empty, the inner
// handler is returned unchanged.
func Compose(inner Handler, mws []Middleware) Handler {
	if inner == nil {
		inner = noopHandler
	}
	h := inner
	for _, mw := range mws {
		if mw == nil {
			continue
		}
		h = mw(h)
	}
	return h
}

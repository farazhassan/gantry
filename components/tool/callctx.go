package tool

import "context"

// callIDKey is the context key carrying the ToolCall.ID currently being
// dispatched.
type callIDKey struct{}

// WithCallID returns a ctx carrying id as the ToolCall.ID currently being
// invoked. The dispatch middleware sets this before calling Tool.Invoke, so
// a Tool implementation (e.g. components/subagent's delegate tool) can read
// which call it is executing under without the Tool interface itself
// needing a wider signature.
func WithCallID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, callIDKey{}, id)
}

// CallIDFrom extracts the ToolCall.ID carried by ctx, if any.
func CallIDFrom(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(callIDKey{}).(string)
	return id, ok
}

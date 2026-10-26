// Package offload keeps large tool results the model has already read out
// of its context. Before each LLM call it replaces a tool result larger than
// Options.MinChars with a short JSON preview once at least Options.AfterCalls
// assistant messages follow it, and stores the full result under a
// content-derived ref. The ReadTool (read_result) lets the model read the
// full result, or parts of it, by that ref or by any "src" handle inside it.
//
// Shrinking right after first use means only the tail of the prompt after
// the result changes, so a provider's prompt cache misses once on that tail
// and every later call reads the smaller preview. Previews depend only on
// the content, so a transcript rebuilt from saved results produces the same
// previews and keeps hitting the cache.
//
// The full results live in a Store scoped by Options.Scope (e.g. a chat
// id), so they outlive the run that produced them. With an empty scope,
// nothing is shrunk.
package offload

// Package vercelai exposes a Gantry agent over the Vercel AI SDK v5 "UI
// Message Stream" protocol -- the SSE-based wire format
// streamText().toUIMessageStreamResponse() and useChat() speak today
// (x-vercel-ai-ui-message-stream: v1) -- as a Server-Sent Events stream.
//
// It is built in three layers, mirroring components/ui/agui:
//
//   - Chunk DTOs + WriteSSE: the UI Message Stream wire types and SSE framing.
//   - Mapper + Sink: Mapper translates the gantry.Event stream into UI
//     Message Stream chunks; Sink adapts a Mapper to a gantry.EventSink
//     that writes SSE frames. Use these directly if you have your own HTTP
//     stack.
//   - Handler: a thin net/http.Handler that decodes a ChatRequest, rebuilds
//     the prior conversation, and drives agent.RunFromStream/ResumeStream.
//
// Scope: the request's replayed message history is honored; Gantry tools
// are server-registered, so there's no client-tools body to ignore the way
// agui ignores client-supplied state/tools. Transport is SSE over HTTP
// POST. The package depends only on the Go standard library, gantry
// itself, and components/ui/internal/streamconfig (shared, generic
// HTTP-streaming plumbing also used by agui).
//
// Deliberately out of scope for v1:
//
//   - Sub-agent / nested-run passthrough. Unlike AG-UI, whose events carry
//     Gantry run identity so a client can demux a parent run interleaved
//     with nested sub-agent runs, the UI Message Stream protocol has no
//     run-graph concept -- a stream constructs a single assistant message,
//     not a thread of runs. Events from a nested run (ev.ParentRunID or
//     ev.ParentToolCallID set) are dropped by Mapper.Map, not translated.
//   - Reasoning round-trip across turns. gantry.Message has no reasoning
//     field (agui has the identical gap), so incoming "reasoning" parts in
//     replayed history are silently dropped, not errored.
//   - Multimodal input. gantry.Message.Content is plain text only. A
//     "file" part inside a user message is a 400 (real content that would
//     otherwise silently vanish); "source-url"/"source-document"/"data-*"/
//     "step-start" parts are silently skipped.
//
// Typical use:
//
//	agent, err := gantry.NewAgent(gantry.WithLLM(llm))
//	if err != nil {
//		// handle error
//	}
//	http.Handle("/vercelai", vercelai.Handler(agent))
//
// Handler is production-hardened out of the box: a run's terminal error is
// logged server-side (slog.Default() by default) as well as streamed to
// the client as an "error" chunk, a panic anywhere in the run is recovered
// into that same clean "error" chunk instead of killing the connection,
// and idle periods get a periodic SSE keep-alive so reverse-proxy/
// load-balancer idle-read timeouts don't sever the connection. See the
// Option functions (WithLogger, WithErrorMapper, WithHeartbeatInterval,
// WithMaxBodyBytes, WithAllowedOrigins) to tune or opt out of any of this.
package vercelai

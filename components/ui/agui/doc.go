// Package agui exposes a Gantry agent over the AG-UI (Agent-User Interaction)
// protocol as a Server-Sent Events stream.
//
// It is built in three layers:
//
//   - Event DTOs + WriteSSE: the AG-UI wire types and SSE framing.
//   - Mapper + Sink: Mapper translates the gantry.Event stream into AG-UI
//     events; Sink adapts a Mapper to a gantry.EventSink that writes SSE frames.
//     Use these directly if you have your own HTTP stack.
//   - Handler: a thin net/http.Handler that decodes a RunAgentInput, rebuilds
//     the prior conversation, and drives agent.RunFromStream.
//
// Scope: the request's replayed message history is honored; client-supplied
// state and tools are ignored. Transport is SSE over HTTP POST. The package
// depends only on the Go standard library and gantry itself.
//
// Typical use:
//
//	agent, err := gantry.NewAgent(gantry.WithLLM(llm))
//	if err != nil {
//		// handle error
//	}
//	http.Handle("/agui", agui.Handler(agent))
//
// Handler is production-hardened out of the box: a run's terminal error is
// logged server-side (slog.Default() by default) as well as streamed to the
// client, a panic anywhere in the run is recovered into a clean RUN_ERROR
// frame instead of killing the connection, and idle periods (a slow tool
// call, a silently-thinking model) get a periodic SSE keep-alive so
// reverse-proxy/load-balancer idle-read timeouts don't sever the connection.
// See the Option functions (WithLogger, WithErrorMapper, WithHeartbeatInterval,
// WithMaxBodyBytes, WithAllowedOrigins) to tune or opt out of any of this.
package agui

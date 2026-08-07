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
// Event coverage: RUN_*, STEP_*, TEXT_MESSAGE_*, TOOL_CALL_*, CUSTOM,
// REASONING_* (Anthropic extended thinking only), RAW (provider frames
// gantry doesn't otherwise model), and ACTIVITY_SNAPSHOT/ACTIVITY_DELTA
// (plan-step progress, via components/planner's update_plan interception).
// Not implemented: STATE_SNAPSHOT/STATE_DELTA/MESSAGES_SNAPSHOT (gantry v1
// tracks no synchronizable agent state — client-supplied state and tools are
// accepted in the request body but ignored, as above), TEXT_MESSAGE_CHUNK/
// TOOL_CALL_CHUNK/REASONING_MESSAGE_CHUNK (redundant alternate encodings of
// the explicit Start/Content/End sequence this package always emits),
// REASONING_ENCRYPTED_VALUE (round-tripping Anthropic's signed thinking
// blocks across turns is a separate correctness feature), and REASONING_*
// for non-Anthropic providers (OpenAI/Ollama/OpenRouter each need their own
// wire-format decoding, not yet done).
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

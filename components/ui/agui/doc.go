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
// Scope: the request's replayed message history and client-declared tools
// (RunAgentInput.Tools — e.g. a CopilotKit frontend action registered via
// useCopilotAction) are honored; client-supplied state (RunAgentInput.State)
// is still ignored. Shared state flows agent-to-client only, as whole
// snapshots (see "Shared state" below). Transport is SSE over HTTP POST. The
// package depends on the Go standard library, gantry itself, and
// components/tool (for the client-tool suspend mechanism used by
// request-declared tools — see input.go and components/tool.DynamicClient).
//
// Event coverage: RUN_*, STEP_*, TEXT_MESSAGE_*, TOOL_CALL_*, CUSTOM,
// REASONING_* (Anthropic extended thinking only), RAW (provider frames
// gantry doesn't otherwise model), ACTIVITY_SNAPSHOT/ACTIVITY_DELTA
// (plan-step progress, via components/planner's update_plan interception),
// and STATE_SNAPSHOT (agent-published shared state, via gantry.SetState).
// Not implemented: STATE_DELTA (state is always sent whole), client-to-agent
// state (accepted in the request body but ignored, as above),
// MESSAGES_SNAPSHOT, TEXT_MESSAGE_CHUNK/
// TOOL_CALL_CHUNK/REASONING_MESSAGE_CHUNK (redundant alternate encodings of
// the explicit Start/Content/End sequence this package always emits),
// REASONING_ENCRYPTED_VALUE (round-tripping Anthropic's signed thinking
// blocks across turns is a separate correctness feature), and REASONING_*
// for non-Anthropic providers (OpenAI/Ollama/OpenRouter each need their own
// wire-format decoding, not yet done).
//
// Shared state: gantry.SetState(ctx, snapshot) emits a gantry.EventStateChanged
// that Mapper translates to STATE_SNAPSHOT {snapshot}. State belongs to the
// AG-UI thread, so only the top-level run's state is forwarded; a nested
// sub-agent run's (ParentRunID/ParentToolCallID set) is dropped. A nil
// snapshot is sent as {}. AG-UI clients keep the last snapshot across runs;
// pass WithRunStateReset to NewMapper/NewSink to emit STATE_SNAPSHOT {} right
// after RUN_STARTED so a run that never calls SetState clears it. The reset
// is off by default, and Handler does not expose it.
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

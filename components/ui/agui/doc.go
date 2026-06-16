// Package agui exposes a Gantry agent over the AG-UI (Agent-User Interaction)
// protocol as a Server-Sent Events stream.
//
// It is built in three layers:
//
//   - Event DTOs + WriteSSE: the AG-UI wire types and SSE framing.
//   - Mapper + Sink: Mapper translates Gantry's harness.Event stream into AG-UI
//     events; Sink adapts a Mapper to a harness.EventSink that writes SSE frames.
//     Use these directly if you have your own HTTP stack.
//   - Handler: a thin net/http.Handler that decodes a RunAgentInput, rebuilds
//     the prior conversation, and drives agent.RunFromStream.
//
// Scope: the request's replayed message history is honored; client-supplied
// state and tools are ignored. Transport is SSE over HTTP POST. The package
// depends only on the Go standard library and Gantry's harness.
//
// Typical use:
//
//	agent, err := harness.New(harness.WithLLM(llm))
//	if err != nil {
//		// handle error
//	}
//	http.Handle("/agui", agui.Handler(agent))
package agui

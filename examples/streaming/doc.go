// Package main streams a gantry agent's whole run as JSON events over Server-
// Sent Events (SSE). It wires a streaming-capable MockLLMClient and the calc
// tool through Agent.RunStream and forwards every Event (phase transitions,
// token deltas, tool calls/results, and the terminal done event) to the HTTP
// response, flushing after each one so a browser sees tokens live. The mock
// keeps it hermetic — no API keys.
//
// Run with:
//
//	go run ./examples/streaming
//
// then, in another terminal:
//
//	curl -N http://localhost:8080/stream
package main

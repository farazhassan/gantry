// Package main demonstrates gantry's signature feature: onion-style middleware.
// It implements a tiny LLMClient by hand (the interface is one method) that
// fails once, then wires two middleware onto PhaseLLMCall — a logging
// middleware that records each call and a retry middleware that re-invokes the
// inner chain on error. It shows the Handler/Middleware types, UseNamed, and
// innermost-first composition (Compose). It is hermetic — no real LLM.
//
// Run with:
//
//	go run ./examples/middleware
package main

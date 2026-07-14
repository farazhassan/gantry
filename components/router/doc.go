// Package router routes incoming requests to one of several registered gantry
// agents.
//
// A Registry is an ordered route table (key, description, *gantry.Agent).
// Classifiers pick a key: RuleRouter for deterministic prefix/regex rules,
// LLMRouter for a one-shot forced-tool-call classification on a designated
// (typically cheap) LLM client, and Chain to try rules first with the LLM as
// backstop. Router glues classification to dispatch: Run and RunFrom resolve
// the winning agent and delegate to its Run/RunFrom, returning the route key
// alongside the agent's terminal state.
//
// Routing is caller-level by design: gantry's phase loop binds exactly one
// LLMClient when resolving PhaseLLMCall, so per-request agent selection
// happens above Agent.Run, not inside it.
package router

// Package session adds keyed, durable multi-turn conversations on top of a
// single shared *gantry.Agent. A Manager pairs the agent with a
// checkpointer.Checkpointer (the durable store); Session(id) returns a handle
// whose Run executes one turn as Load(id) -> agent.RunFrom(prior, input) ->
// Save(id). The store is the single source of truth, so a fresh Manager over
// the same store and id resumes a conversation transparently — including across
// process restarts when backed by a durable Checkpointer. RunStream is the
// streaming counterpart: same Load/Save contract, but it also emits whole-run
// Events to an EventSink for forwarding over SSE.
//
// Memory vs. Session. Both carry a transcript across runs, but they are used
// alternatively, never stacked for the same transcript:
//
//   - memory  — implicit, single, unkeyed transcript baked into one agent via
//     memory.New. Best for one long-lived in-process conversation.
//   - session — explicit, keyed, durable; the transcript lives in the per-id
//     State carried by RunFrom. Best for many conversations and/or persistence.
//
// The agent attached to a Manager MUST NOT also carry memory.New or
// checkpointer.New: the Session owns load/save, and memory would double-manage
// the transcript.
package session

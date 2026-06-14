// Package main shows how to run one agent across multiple turns with the
// session package. A Manager pairs a shared agent with a checkpointer store;
// Session("user-42").Run executes a turn as Load -> RunFrom -> Save, so each
// turn sees the prior transcript and tokens accumulate. The example then opens a
// SECOND Manager over the same store and id to show durable resume — what
// cross-process continuation looks like with a real durable backend. The agent
// carries no memory or checkpointer middleware; the Session owns the transcript.
// It uses a scripted MockLLMClient so it is hermetic — continuity is shown by
// the transcript carrying forward (a real model would use that context to
// answer).
//
// Run with:
//
//	go run ./examples/session
package main

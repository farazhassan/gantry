// Package main demonstrates checkpointer.Lease and checkpointer.ResumeLocked
// across four multi-worker scenarios, each isolating one safety property:
//
//   - RunExample: worker A abandons a run while holding its lease (a
//     crash — no heartbeat, no release). Worker B's immediate takeover is
//     rejected with checkpointer.ErrLeaseHeld while the lease is still
//     live, and succeeds once it expires, resuming from a mid-run
//     checkpoint rather than from scratch.
//   - RunGracefulHandoff: worker A finishes and releases its lease
//     cleanly instead of crashing, so worker B can take over immediately
//     — no TTL wait required. Contrast with RunExample.
//   - RunLiveWorkerBlocksTakeover: worker B is still genuinely working —
//     its LLM call is held open well past the nominal lease TTL — and a
//     concurrent takeover attempt by worker C is still rejected, because
//     checkpointer.KeepAlive has been renewing worker B's lease in the
//     background the whole time. This is why ResumeLocked renews in the
//     background instead of just granting a long TTL up front: a slow but
//     live worker is never mistaken for a crashed one.
//   - RunLeaseLostCancelsRun: worker A's lease is lost mid-run (its
//     heartbeat fails to renew — e.g. a network partition, or a bug that
//     lets another worker wrongly reclaim it). ResumeLocked cancels the
//     in-flight run rather than leaving it running un-owned, which could
//     otherwise race a second worker over the same state.
//
// All four use a scripted MockLLMClient and the in-memory checkpointer/mem
// backends, so they are hermetic.
//
// Run with:
//
//	go run ./examples/checkpoint-resume
package main

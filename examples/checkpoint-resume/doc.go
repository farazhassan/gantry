// Package main demonstrates the multi-worker crash-recovery path added by
// mid-run checkpointing and checkpointer.Lease: worker A abandons a run
// while holding its lease (simulating a crash — no heartbeat, no release).
// Worker B's first takeover attempt is rejected with checkpointer.ErrLeaseHeld
// while the lease is still live, and succeeds once it has expired, resuming
// from a mid-run checkpoint rather than from scratch. It uses a scripted
// MockLLMClient and the in-memory checkpointer/mem backends, so it is
// hermetic.
//
// Run with:
//
//	go run ./examples/checkpoint-resume
package main

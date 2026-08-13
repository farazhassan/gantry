// Package etcd provides a checkpointer.Lease backed by etcd. Because etcd's
// native Lease TTL only has whole-second resolution and the server enforces
// its own multi-second minimum, expiry is instead judged against an
// application-level deadline stored in each key's value, mutated through
// optimistic-concurrency transactions (compare-and-swap on the key's
// revision) — see the Lease type's doc comment for the full design and its
// clock-skew caveat. A native etcd Lease is still attached to each key as a
// non-load-bearing backstop so an abandoned lease is eventually cleaned up.
//
// Run with:
//
//	go get github.com/farazhassan/gantry/components/checkpointer/etcd
package etcd

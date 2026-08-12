// Package etcd provides a checkpointer.Lease backed by etcd, using its
// native lease API (Grant/KeepAliveOnce/Revoke) plus a transactional put
// for atomic acquire.
//
// Run with:
//
//	go get github.com/farazhassan/gantry/components/checkpointer/etcd
package etcd

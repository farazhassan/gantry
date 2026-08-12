package redis_test

import (
	"testing"

	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/components/checkpointer/redis"
	"github.com/farazhassan/gantry/conformance"
)

var _ checkpointer.Lease = (*redis.Lease)(nil)

func TestLeaseConformance(t *testing.T) {
	rdb := newClient(t) // defined in store_test.go, same package
	conformance.LeaseSuite(t, func() checkpointer.Lease { return redis.NewLease(rdb) })
}

package mem_test

import (
	"testing"

	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/components/checkpointer/mem"
	"github.com/farazhassan/gantry/conformance"
)

func TestLeaseConformance(t *testing.T) {
	conformance.LeaseSuite(t, func() checkpointer.Lease { return mem.NewLease() })
}

func TestLeaseImplementsInterface(t *testing.T) {
	var _ checkpointer.Lease = mem.NewLease()
}

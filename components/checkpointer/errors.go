package checkpointer

import "errors"

// ErrNotFound is returned (wrapped) by a Checkpointer's Load when no state
// exists for the given id. Callers detect it with errors.Is. Third-party stores
// should wrap this sentinel so callers can distinguish "no such id" from a real
// backend error.
var ErrNotFound = errors.New("gantry: checkpoint not found")

// ErrLeaseHeld is returned (wrapped) by Lease.Acquire when another owner
// currently holds the lease for the given id.
var ErrLeaseHeld = errors.New("gantry: lease held by another owner")

// ErrLeaseLost is returned (wrapped) by Lease.Renew or Lease.Release when
// the supplied token no longer identifies the current holder — it expired,
// or another worker reclaimed the id after expiry.
var ErrLeaseLost = errors.New("gantry: lease lost")

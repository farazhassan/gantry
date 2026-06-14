package checkpointer

import "errors"

// ErrNotFound is returned (wrapped) by a Checkpointer's Load when no state
// exists for the given id. Callers detect it with errors.Is. Third-party stores
// should wrap this sentinel so callers can distinguish "no such id" from a real
// backend error.
var ErrNotFound = errors.New("gantry: checkpoint not found")

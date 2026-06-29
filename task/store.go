package task

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotFound is returned (wrapped) by a TaskStore's LoadTask when no task
// exists for the given id. Callers detect it with errors.Is. Third-party stores
// should wrap this sentinel so callers can distinguish "no such id" from a real
// backend error. This mirrors checkpointer.ErrNotFound.
var ErrNotFound = errors.New("gantry/task: task not found")

// errWrap is the canonical "not found for id" wrapping that store
// implementations use, kept here so the contract is defined in one place.
func errWrap(sentinel error, id string) error {
	return fmt.Errorf("%w: id %q", sentinel, id)
}

// TaskStore persists and restores Tasks by id and enumerates a session's tasks.
// It is the durable home for cross-run work state. Implementations MUST be safe
// for concurrent use and MUST wrap ErrNotFound from LoadTask for unknown ids.
type TaskStore interface {
	SaveTask(ctx context.Context, t *Task) error
	LoadTask(ctx context.Context, id string) (*Task, error)
	ListBySession(ctx context.Context, sessionID string) ([]TaskRef, error)
}

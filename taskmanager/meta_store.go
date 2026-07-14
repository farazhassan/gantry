package taskmanager

import (
	"context"
	"errors"

	"github.com/farazhassan/gantry/task"
)

// MetaStore persists SessionMeta keyed by session id. It mirrors task.TaskStore:
// a single key space, with implementations free to deep-copy so callers cannot
// mutate stored state by reference.
//
// Implementations MUST be safe for concurrent use: ActiveTask reads meta
// without the per-session lock, concurrently with a drive's SaveMeta calls.
// InMemoryMetaStore satisfies this (mutex-guarded, deep-copying on save and
// load); durable implementations must do the same.
type MetaStore interface {
	LoadMeta(ctx context.Context, sessionID string) (*task.SessionMeta, error)
	SaveMeta(ctx context.Context, sessionID string, m *task.SessionMeta) error
}

// ErrMetaNotFound is returned by LoadMeta when a session has no meta yet.
var ErrMetaNotFound = errors.New("taskmanager: session meta not found")

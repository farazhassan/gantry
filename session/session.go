package session

import (
	"sync"

	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/harness"
)

// Session is a keyed handle to one durable conversation. It is safe for
// concurrent use: turns for a given id are serialized by its mutex.
type Session struct {
	id    string
	agent *harness.Agent
	store checkpointer.Checkpointer
	mu    sync.Mutex
}

// ID returns the session's id.
func (s *Session) ID() string { return s.id }

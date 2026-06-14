package session

import (
	"sync"

	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/harness"
)

// Manager hands out keyed Session handles backed by one shared agent and store.
// It is safe for concurrent use.
type Manager struct {
	agent    *harness.Agent
	store    checkpointer.Checkpointer
	mu       sync.Mutex
	sessions map[string]*Session
}

// NewManager pairs a shared agent with a durable store. It panics if agent or
// store is nil (programmer error), matching the lightweight constructors
// elsewhere in the repo. The agent MUST NOT carry WithMemory or
// WithCheckpointer (see package doc).
func NewManager(a *harness.Agent, store checkpointer.Checkpointer) *Manager {
	if a == nil {
		panic("gantry/session: NewManager requires a non-nil agent")
	}
	if store == nil {
		panic("gantry/session: NewManager requires a non-nil store")
	}
	return &Manager{
		agent:    a,
		store:    store,
		sessions: map[string]*Session{},
	}
}

// Session returns a get-or-create handle for id. Concurrency-safe. The handle is
// cached so that the per-session mutex is shared across callers using the same
// id within this process.
func (m *Manager) Session(id string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok {
		return s
	}
	s := &Session{id: id, agent: m.agent, store: m.store}
	m.sessions[id] = s
	return s
}

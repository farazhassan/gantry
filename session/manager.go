package session

import (
	"sync"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/checkpointer"
)

// Resolver maps a transfer handoff to its target agent. sessionID identifies
// the session the handoff happened in; h is the handoff the run terminated
// with (never nil, Mode == gantry.HandoffTransfer). Returning nil means
// "unknown target" and fails the turn with ErrHandoffTargetUnknown.
type Resolver func(sessionID string, h *gantry.Handoff) *gantry.Agent

// Option configures a Manager at construction.
type Option func(*Manager)

// WithResolver enables transfer-handoff routing. When a turn's run terminates
// with state.DoneReason == gantry.DoneHandoff and state.Handoff.Mode ==
// gantry.HandoffTransfer, the Session resolves the target agent through f and
// re-runs the same turn on it from the accumulated transcript (bounded; see
// Session run docs). The default — no resolver — keeps the prior behavior: a
// DoneHandoff state is saved and returned as-is. Delegate-mode handoffs are
// never routed here (the subagent-delegate plan covers delegation).
func WithResolver(f Resolver) Option {
	return func(m *Manager) { m.resolver = f }
}

// Manager hands out keyed Session handles backed by one shared agent and store.
// It is safe for concurrent use.
type Manager struct {
	agent    *gantry.Agent
	store    checkpointer.Checkpointer
	resolver Resolver // nil ⇒ no transfer-handoff routing
	mu       sync.Mutex
	sessions map[string]*Session
}

// NewManager pairs a shared agent with a durable store. It panics if agent or
// store is nil (programmer error), matching the lightweight constructors
// elsewhere in the repo. The agent MUST NOT carry memory.New or
// checkpointer.New (see package doc).
func NewManager(a *gantry.Agent, store checkpointer.Checkpointer, opts ...Option) *Manager {
	if a == nil {
		panic("gantry/session: NewManager requires a non-nil agent")
	}
	if store == nil {
		panic("gantry/session: NewManager requires a non-nil store")
	}
	m := &Manager{
		agent:    a,
		store:    store,
		sessions: map[string]*Session{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	return m
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
	s := &Session{id: id, agent: m.agent, store: m.store, resolver: m.resolver}
	m.sessions[id] = s
	return s
}

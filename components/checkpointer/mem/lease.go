package mem

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/farazhassan/gantry/components/checkpointer"
)

// Lease is a process-local, in-memory checkpointer.Lease. Useful for tests
// and hermetic examples; it cannot coordinate real separate processes.
type Lease struct {
	mu      sync.Mutex
	holders map[string]leaseEntry
}

type leaseEntry struct {
	token   string
	expires time.Time
}

// NewLease returns an empty in-memory Lease.
func NewLease() *Lease { return &Lease{holders: map[string]leaseEntry{}} }

func (l *Lease) Acquire(_ context.Context, id string, ttl time.Duration) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.holders[id]; ok && time.Now().Before(e.expires) {
		return "", fmt.Errorf("%w: id %q", checkpointer.ErrLeaseHeld, id)
	}
	tok, err := randomToken()
	if err != nil {
		return "", err
	}
	l.holders[id] = leaseEntry{token: tok, expires: time.Now().Add(ttl)}
	return tok, nil
}

func (l *Lease) Renew(_ context.Context, id, token string, ttl time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.holders[id]
	if !ok || e.token != token || !time.Now().Before(e.expires) {
		return fmt.Errorf("%w: id %q", checkpointer.ErrLeaseLost, id)
	}
	e.expires = time.Now().Add(ttl)
	l.holders[id] = e
	return nil
}

func (l *Lease) Release(_ context.Context, id, token string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.holders[id]
	if !ok || e.token != token || !time.Now().Before(e.expires) {
		return fmt.Errorf("%w: id %q", checkpointer.ErrLeaseLost, id)
	}
	delete(l.holders, id)
	return nil
}

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

var _ checkpointer.Lease = (*Lease)(nil)

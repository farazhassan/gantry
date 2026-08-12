package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/farazhassan/gantry/components/checkpointer"
)

// Lease is a Redis-backed checkpointer.Lease: a single-instance
// compare-and-swap mutex keyed by run id. Each held lease is a Redis hash
// with a "token" field (the fencing token returned from Acquire) and a
// "deadline" field (a Unix-millisecond timestamp). Acquire/Renew/Release
// run as Lua scripts that judge expiry against the Redis server's own
// clock (redis.call("TIME")) rather than a native key TTL: this keeps
// expiry decisions consistent regardless of client-server clock skew, and
// — practically — miniredis (used in tests) only decrements native key
// TTLs via an explicit FastForward call, not real elapsed time, so an
// implementation relying on native TTL for expiry cannot be exercised by
// ordinary time.Sleep-based tests. A native PEXPIRE is additionally set on
// every write as a memory-hygiene backstop so an abandoned lease key
// doesn't linger in Redis forever, but it is not load bearing for
// correctness — the "deadline" field inside the hash is authoritative.
//
// This is NOT a Redlock multi-master implementation — safe against a
// worker process dying, not against the Redis instance itself
// partitioning.
type Lease struct {
	rdb    goredis.Cmdable
	prefix string
}

// LeaseOption configures a Lease.
type LeaseOption func(*Lease)

// WithLeaseKeyPrefix sets the prefix prepended to every id (default "gantry:lease:").
func WithLeaseKeyPrefix(prefix string) LeaseOption { return func(l *Lease) { l.prefix = prefix } }

// NewLease returns a Lease backed by rdb. rdb must be non-nil; the caller
// owns connection setup, TLS, auth, and pooling.
func NewLease(rdb goredis.Cmdable, opts ...LeaseOption) *Lease {
	l := &Lease{rdb: rdb, prefix: "gantry:lease:"}
	for _, o := range opts {
		o(l)
	}
	return l
}

func (l *Lease) key(id string) string { return l.prefix + id }

// nowMsLua computes the Redis server's current time in Unix milliseconds
// into a local "nowMs", anchored to the server's own TIME command rather
// than any client clock (see the Lease doc comment for why). It is the
// crux of every script below, so it is defined exactly once here and
// prepended everywhere, instead of copy-pasted per script where an edit
// could update some copies and silently miss another.
const nowMsLua = `
local t = redis.call("TIME")
local nowMs = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
`

// requireHolderLua computes nowMs (via nowMsLua) and then validates that
// "token" (already declared by the caller) is the current holder of "key"
// (also already declared) and that its lease has not passed its deadline.
// It returns 0 (via an early "return") when that check fails, otherwise
// falls through with curToken/deadline/nowMs all in scope so the caller
// can perform its own state change (renew's PEXPIRE/HSET, release's DEL).
// renewScript and releaseScript share this identical validity check.
const requireHolderLua = nowMsLua + `
local curToken = redis.call("HGET", key, "token")
local deadline = redis.call("HGET", key, "deadline")
if curToken ~= token or not deadline or tonumber(deadline) <= nowMs then
	return 0
end
`

var acquireScript = goredis.NewScript(`
local key = KEYS[1]
local token = ARGV[1]
local ttlMs = tonumber(ARGV[2])
` + nowMsLua + `
local deadline = redis.call("HGET", key, "deadline")
if deadline and tonumber(deadline) > nowMs then
	return 0
end
redis.call("HSET", key, "token", token, "deadline", tostring(nowMs + ttlMs))
redis.call("PEXPIRE", key, ttlMs * 2)
return 1
`)

var renewScript = goredis.NewScript(`
local key = KEYS[1]
local token = ARGV[1]
local ttlMs = tonumber(ARGV[2])
` + requireHolderLua + `
redis.call("HSET", key, "deadline", tostring(nowMs + ttlMs))
redis.call("PEXPIRE", key, ttlMs * 2)
return 1
`)

var releaseScript = goredis.NewScript(`
local key = KEYS[1]
local token = ARGV[1]
` + requireHolderLua + `
redis.call("DEL", key)
return 1
`)

func (l *Lease) Acquire(ctx context.Context, id string, ttl time.Duration) (string, error) {
	tok, err := randomToken()
	if err != nil {
		return "", fmt.Errorf("checkpointer/redis: generate token: %w", err)
	}
	n, err := acquireScript.Run(ctx, l.rdb, []string{l.key(id)}, tok, ttl.Milliseconds()).Int()
	if err != nil {
		return "", fmt.Errorf("checkpointer/redis: acquire %q: %w", id, err)
	}
	if n == 0 {
		return "", fmt.Errorf("%w: id %q", checkpointer.ErrLeaseHeld, id)
	}
	return tok, nil
}

func (l *Lease) Renew(ctx context.Context, id, token string, ttl time.Duration) error {
	n, err := renewScript.Run(ctx, l.rdb, []string{l.key(id)}, token, ttl.Milliseconds()).Int()
	if err != nil {
		return fmt.Errorf("checkpointer/redis: renew %q: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id %q", checkpointer.ErrLeaseLost, id)
	}
	return nil
}

func (l *Lease) Release(ctx context.Context, id, token string) error {
	n, err := releaseScript.Run(ctx, l.rdb, []string{l.key(id)}, token).Int()
	if err != nil {
		return fmt.Errorf("checkpointer/redis: release %q: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id %q", checkpointer.ErrLeaseLost, id)
	}
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

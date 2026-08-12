package etcd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/farazhassan/gantry/components/checkpointer"
)

// Lease is an etcd-backed checkpointer.Lease.
//
// etcd's native Lease API (Grant/KeepAliveOnce/Revoke) cannot, by itself,
// implement this type: Grant's TTL is a whole number of seconds
// (etcdserverpb.LeaseGrantRequest.TTL is an int64 count of seconds), and
// the etcd server additionally enforces its own minimum — computed from
// the cluster's election-timeout config, ~1-2s in a default deployment —
// bumping anything smaller up to that floor. checkpointer.Lease has no
// such floor (see conformance.LeaseSuite, which exercises TTLs as small as
// 30ms), so a native-TTL-only implementation cannot pass it.
//
// Instead, each held lease is an etcd key whose value packs the fencing
// token, an application-level deadline (a Unix-nanosecond timestamp), and
// the id of a backing native etcd Lease, e.g. "a1b2c3|1699999999000000000|7".
// Acquire/Renew/Release read that value and judge expiry against the
// deadline field themselves (using this process's wall clock — see the
// package-level caveat about clock skew below), then apply their change
// with an etcd transaction guarded by a compare-and-swap on the key's mod
// revision (or, for a first Acquire, on CreateRevision == 0), retrying on
// a lost race. This mirrors the sibling redis implementation's approach of
// treating a self-computed deadline as authoritative, just via optimistic
// concurrency control instead of a server-side Lua script (etcd has no
// server-side scripting).
//
// The native etcd Lease attached to each key (well over the requested
// ttl, refreshed on every successful Renew) is not load-bearing for
// correctness — Acquire already reclaims a key whose embedded deadline has
// passed, however long the physical key has lingered. It exists purely so
// an abandoned lease (a holder that crashes and never calls Release, and
// whose id is never Acquired again) doesn't stay in etcd forever.
//
// Because expiry is judged against this process's local clock rather than
// the etcd server's, Lease is only as safe against clock skew as the
// machines running it: unlike Redis's TIME command, etcd does not expose
// a simple RPC for "the server's current time" that every request could
// anchor to instead.
type Lease struct {
	cli    *clientv3.Client
	prefix string
}

// Option configures a Lease.
type Option func(*Lease)

// WithKeyPrefix sets the prefix prepended to every id (default "gantry/lease/").
func WithKeyPrefix(prefix string) Option { return func(l *Lease) { l.prefix = prefix } }

// New returns a Lease backed by cli. cli must be non-nil; the caller owns
// its lifecycle (endpoints, TLS, auth, Close).
func New(cli *clientv3.Client, opts ...Option) *Lease {
	l := &Lease{cli: cli, prefix: "gantry/lease/"}
	for _, o := range opts {
		o(l)
	}
	return l
}

func (l *Lease) key(id string) string { return l.prefix + id }

// backstopTTLSeconds picks the TTL, in whole seconds, for the native etcd
// Lease backing a key: generous relative to ttl so ordinary renewal (which
// also refreshes this native lease) keeps comfortably ahead of it, but not
// load-bearing for correctness — see the Lease doc comment.
func backstopTTLSeconds(ttl time.Duration) int64 {
	secs := int64((4 * ttl).Seconds())
	if secs < 1 {
		secs = 1
	}
	return secs
}

// encodeValue packs a held lease's fencing token, application-level
// deadline, and backing native-lease id into a key's value.
func encodeValue(token string, deadline time.Time, leaseID clientv3.LeaseID) string {
	return token + "|" + strconv.FormatInt(deadline.UnixNano(), 10) + "|" + strconv.FormatInt(int64(leaseID), 10)
}

func decodeValue(raw string) (token string, deadline time.Time, leaseID clientv3.LeaseID, err error) {
	parts := strings.SplitN(raw, "|", 3)
	if len(parts) != 3 {
		return "", time.Time{}, 0, fmt.Errorf("malformed lease value %q", raw)
	}
	deadlineNs, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", time.Time{}, 0, fmt.Errorf("malformed lease deadline %q: %w", raw, err)
	}
	idn, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return "", time.Time{}, 0, fmt.Errorf("malformed lease id %q: %w", raw, err)
	}
	return parts[0], time.Unix(0, deadlineNs), clientv3.LeaseID(idn), nil
}

// held reports whether deadline is still in the future.
func held(deadline time.Time) bool { return time.Now().Before(deadline) }

// maxAcquireAttempts bounds the read-decide-CAS retry loop in Acquire so
// contention with other Acquire callers for the same id can't spin
// forever; a real winner or a genuine ErrLeaseHeld is expected within a
// handful of attempts.
const maxAcquireAttempts = 10

// getHeld reads id's key, verifies it is currently held by token and has
// not passed its embedded deadline, and returns the decoded lease's
// backing native-lease id plus the key's current ModRevision (for the
// caller's own CAS write). It returns checkpointer.ErrLeaseLost (wrapped
// with id) if the key is absent, the token doesn't match, or the deadline
// has passed — the shared preamble of Renew and Release, which otherwise
// differ only in what they do with a confirmed-held lease (renew it vs.
// delete it).
func (l *Lease) getHeld(ctx context.Context, id, token string) (leaseID clientv3.LeaseID, modRevision int64, err error) {
	key := l.key(id)
	getResp, err := l.cli.Get(ctx, key)
	if err != nil {
		return 0, 0, fmt.Errorf("checkpointer/etcd: %q: %w", id, err)
	}
	if len(getResp.Kvs) == 0 {
		return 0, 0, fmt.Errorf("%w: id %q", checkpointer.ErrLeaseLost, id)
	}
	curToken, deadline, leaseID, err := decodeValue(string(getResp.Kvs[0].Value))
	if err != nil {
		return 0, 0, fmt.Errorf("checkpointer/etcd: %q: %w", id, err)
	}
	if curToken != token || !held(deadline) {
		return 0, 0, fmt.Errorf("%w: id %q", checkpointer.ErrLeaseLost, id)
	}
	return leaseID, getResp.Kvs[0].ModRevision, nil
}

func (l *Lease) Acquire(ctx context.Context, id string, ttl time.Duration) (string, error) {
	key := l.key(id)
	tok, err := randomToken()
	if err != nil {
		return "", fmt.Errorf("checkpointer/etcd: generate token: %w", err)
	}

	for attempt := 0; attempt < maxAcquireAttempts; attempt++ {
		getResp, err := l.cli.Get(ctx, key)
		if err != nil {
			return "", fmt.Errorf("checkpointer/etcd: acquire %q: %w", id, err)
		}

		var cmp clientv3.Cmp
		var staleLeaseID clientv3.LeaseID
		if len(getResp.Kvs) == 0 {
			cmp = clientv3.Compare(clientv3.CreateRevision(key), "=", 0)
		} else {
			_, deadline, leaseID, derr := decodeValue(string(getResp.Kvs[0].Value))
			if derr != nil {
				return "", fmt.Errorf("checkpointer/etcd: acquire %q: %w", id, derr)
			}
			if held(deadline) {
				return "", fmt.Errorf("%w: id %q", checkpointer.ErrLeaseHeld, id)
			}
			staleLeaseID = leaseID
			cmp = clientv3.Compare(clientv3.ModRevision(key), "=", getResp.Kvs[0].ModRevision)
		}

		grant, err := l.cli.Grant(ctx, backstopTTLSeconds(ttl))
		if err != nil {
			return "", fmt.Errorf("checkpointer/etcd: grant lease for %q: %w", id, err)
		}
		val := encodeValue(tok, time.Now().Add(ttl), grant.ID)

		txnResp, err := l.cli.Txn(ctx).
			If(cmp).
			Then(clientv3.OpPut(key, val, clientv3.WithLease(grant.ID))).
			Commit()
		if err != nil {
			_, _ = l.cli.Revoke(ctx, grant.ID) // don't leak the lease we just granted
			return "", fmt.Errorf("checkpointer/etcd: acquire txn %q: %w", id, err)
		}
		if !txnResp.Succeeded {
			_, _ = l.cli.Revoke(ctx, grant.ID) // lost the race; don't leak this attempt's lease
			continue
		}
		if staleLeaseID != 0 {
			_, _ = l.cli.Revoke(ctx, staleLeaseID) // best-effort: clean up the expired holder's backstop
		}
		return tok, nil
	}
	return "", fmt.Errorf("checkpointer/etcd: acquire %q: exceeded %d attempts under contention", id, maxAcquireAttempts)
}

func (l *Lease) Renew(ctx context.Context, id, token string, ttl time.Duration) error {
	leaseID, modRevision, err := l.getHeld(ctx, id, token)
	if err != nil {
		return err
	}

	// Push the backing native lease's own physical expiry out too, so it
	// can never fire while the application-level deadline set below is
	// still in the future.
	if _, err := l.cli.KeepAliveOnce(ctx, leaseID); err != nil {
		if errors.Is(err, rpctypes.ErrLeaseNotFound) {
			// The backstop lease is already gone, which took the key with
			// it: equivalent to having lost the lease outright.
			return fmt.Errorf("%w: id %q", checkpointer.ErrLeaseLost, id)
		}
		return fmt.Errorf("checkpointer/etcd: renew %q: %w", id, err)
	}

	key := l.key(id)
	newVal := encodeValue(token, time.Now().Add(ttl), leaseID)
	txnResp, err := l.cli.Txn(ctx).
		If(clientv3.Compare(clientv3.ModRevision(key), "=", modRevision)).
		Then(clientv3.OpPut(key, newVal, clientv3.WithLease(leaseID))).
		Commit()
	if err != nil {
		return fmt.Errorf("checkpointer/etcd: renew %q: %w", id, err)
	}
	if !txnResp.Succeeded {
		return fmt.Errorf("%w: id %q", checkpointer.ErrLeaseLost, id)
	}
	return nil
}

func (l *Lease) Release(ctx context.Context, id, token string) error {
	leaseID, modRevision, err := l.getHeld(ctx, id, token)
	if err != nil {
		return err
	}

	key := l.key(id)
	txnResp, err := l.cli.Txn(ctx).
		If(clientv3.Compare(clientv3.ModRevision(key), "=", modRevision)).
		Then(clientv3.OpDelete(key)).
		Commit()
	if err != nil {
		return fmt.Errorf("checkpointer/etcd: release %q: %w", id, err)
	}
	if !txnResp.Succeeded {
		return fmt.Errorf("%w: id %q", checkpointer.ErrLeaseLost, id)
	}
	_, _ = l.cli.Revoke(ctx, leaseID) // best-effort; the transaction above already deleted the key
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

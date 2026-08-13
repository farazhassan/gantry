package etcd_test

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"testing"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"

	"github.com/farazhassan/gantry/components/checkpointer"
	gantryetcd "github.com/farazhassan/gantry/components/checkpointer/etcd"
	"github.com/farazhassan/gantry/conformance"
)

// freePort asks the OS for a port that's free right now. There's a small
// window between closing this listener and etcd binding the same port
// where another process could grab it first, but that's the standard,
// accepted tradeoff for hermetic parallel tests — it's a vast improvement
// over the fixed default ports (2379/2380), which conflict deterministically
// (not just theoretically) whenever this test's package runs alongside
// another one that also starts embedded etcd.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func mustParseURL(t *testing.T, raw string) url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url %q: %v", raw, err)
	}
	return *u
}

func newTestClient(t *testing.T) *clientv3.Client {
	t.Helper()

	cfg := embed.NewConfig()
	cfg.Dir = t.TempDir()
	cfg.LogLevel = "error"

	peerURL := mustParseURL(t, fmt.Sprintf("http://127.0.0.1:%d", freePort(t)))
	clientURL := mustParseURL(t, fmt.Sprintf("http://127.0.0.1:%d", freePort(t)))
	cfg.ListenPeerUrls = []url.URL{peerURL}
	cfg.AdvertisePeerUrls = []url.URL{peerURL}
	cfg.ListenClientUrls = []url.URL{clientURL}
	cfg.AdvertiseClientUrls = []url.URL{clientURL}
	cfg.InitialCluster = fmt.Sprintf("%s=%s", cfg.Name, peerURL.String())

	e, err := embed.StartEtcd(cfg)
	if err != nil {
		t.Fatalf("start embedded etcd: %v", err)
	}
	t.Cleanup(e.Close)

	select {
	case <-e.Server.ReadyNotify():
	case <-time.After(15 * time.Second):
		t.Fatal("embedded etcd did not become ready in time")
	}

	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{e.Clients[0].Addr().String()},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("new etcd client: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

var _ checkpointer.Lease = (*gantryetcd.Lease)(nil)

func TestLeaseConformance(t *testing.T) {
	cli := newTestClient(t)
	conformance.LeaseSuite(t, func() checkpointer.Lease { return gantryetcd.New(cli) })
}

func TestLeaseKeyPrefixIsApplied(t *testing.T) {
	cli := newTestClient(t)
	l := gantryetcd.New(cli, gantryetcd.WithKeyPrefix("myapp/lease/"))
	ctx := context.Background()

	if _, err := l.Acquire(ctx, "id-1", time.Minute); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	resp, err := cli.Get(ctx, "myapp/lease/id-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(resp.Kvs) != 1 {
		t.Fatalf("expected key stored under prefix, got %d matching keys", len(resp.Kvs))
	}
}

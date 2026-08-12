package etcd_test

import (
	"testing"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"

	"github.com/farazhassan/gantry/components/checkpointer"
	gantryetcd "github.com/farazhassan/gantry/components/checkpointer/etcd"
	"github.com/farazhassan/gantry/conformance"
)

func newTestClient(t *testing.T) *clientv3.Client {
	t.Helper()

	cfg := embed.NewConfig()
	cfg.Dir = t.TempDir()
	cfg.LogLevel = "error"

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

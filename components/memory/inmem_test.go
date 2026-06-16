package memory_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/memory"
)

func TestInMemoryStoreAppendAndRead(t *testing.T) {
	m := memory.NewInMemoryStore()
	ctx := context.Background()

	if err := m.Append(ctx, gantry.Message{Role: gantry.RoleUser, Content: "hi"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := m.Append(ctx, gantry.Message{Role: gantry.RoleAssistant, Content: "hello"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	msgs, err := m.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len(msgs) = %d, want 2", len(msgs))
	}
	if msgs[0].Content != "hi" || msgs[1].Content != "hello" {
		t.Errorf("unexpected order: %+v", msgs)
	}
}

func TestInMemoryStoreReadReturnsCopy(t *testing.T) {
	m := memory.NewInMemoryStore()
	m.Append(context.Background(), gantry.Message{Content: "x"})
	a, _ := m.Read(context.Background())
	a[0].Content = "mutated"
	b, _ := m.Read(context.Background())
	if b[0].Content != "x" {
		t.Errorf("Read returned aliased slice; got %q", b[0].Content)
	}
}

func TestInMemoryStoreInterfaceSatisfaction(t *testing.T) {
	var _ memory.Memory = memory.NewInMemoryStore()
}

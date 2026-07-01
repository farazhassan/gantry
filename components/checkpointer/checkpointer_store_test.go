package checkpointer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/checkpointer"
)

func richState() *gantry.State {
	return &gantry.State{
		System:   "sys",
		Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}},
		Usage:    gantry.Usage{InputTokens: 10, OutputTokens: 5},
		Meta:     map[string]any{"k": "v"},
	}
}

func TestStoreCheckpointer_FullRoundTrip(t *testing.T) {
	c, err := checkpointer.FromStore(checkpointer.NewMemStore())
	if err != nil {
		t.Fatalf("FromStore: %v", err)
	}
	if err := c.Save(context.Background(), "id", richState()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := c.Load(context.Background(), "id")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.System != "sys" || len(got.Messages) != 1 || got.Usage.InputTokens != 10 || got.Meta["k"] != "v" {
		t.Fatalf("round-trip mismatch: %#v", got)
	}
}

func TestStoreCheckpointer_StoreOnlyKeepsListedFields(t *testing.T) {
	c, _ := checkpointer.FromStore(checkpointer.NewMemStore(),
		checkpointer.StoreOnly(checkpointer.FieldUsage, checkpointer.FieldMeta))
	_ = c.Save(context.Background(), "id", richState())
	got, _ := c.Load(context.Background(), "id")
	if got.Usage.InputTokens != 10 || got.Meta["k"] != "v" {
		t.Fatalf("kept fields lost: %#v", got)
	}
	if got.System != "" || got.Messages != nil {
		t.Fatalf("dropped fields present: System=%q Messages=%v", got.System, got.Messages)
	}
}

func TestStoreCheckpointer_OmitDropsListedFields(t *testing.T) {
	c, _ := checkpointer.FromStore(checkpointer.NewMemStore(),
		checkpointer.Omit(checkpointer.FieldMessages))
	_ = c.Save(context.Background(), "id", richState())
	got, _ := c.Load(context.Background(), "id")
	if got.Messages != nil {
		t.Fatalf("Messages should be dropped, got %v", got.Messages)
	}
	if got.System != "sys" || got.Usage.InputTokens != 10 {
		t.Fatalf("non-omitted fields lost: %#v", got)
	}
}

func TestStoreCheckpointer_SaveNilStateErrors(t *testing.T) {
	c, _ := checkpointer.FromStore(checkpointer.NewMemStore())
	if err := c.Save(context.Background(), "id", nil); err == nil {
		t.Fatal("want error saving nil state")
	}
}

func TestStoreCheckpointer_LoadMissingIsErrNotFound(t *testing.T) {
	c, _ := checkpointer.FromStore(checkpointer.NewMemStore())
	_, err := c.Load(context.Background(), "ghost")
	if !errors.Is(err, checkpointer.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestFromStore_NilStoreErrors(t *testing.T) {
	if _, err := checkpointer.FromStore(nil); err == nil {
		t.Fatal("want error for nil store")
	}
}

func TestFromStore_BothProjectionsErrors(t *testing.T) {
	_, err := checkpointer.FromStore(checkpointer.NewMemStore(),
		checkpointer.StoreOnly(checkpointer.FieldUsage),
		checkpointer.Omit(checkpointer.FieldMessages))
	if err == nil {
		t.Fatal("want error when both StoreOnly and Omit are set")
	}
}

func TestStoreCheckpointer_ImplementsCheckpointer(t *testing.T) {
	c, _ := checkpointer.FromStore(checkpointer.NewMemStore())
	var _ checkpointer.Checkpointer = c
}

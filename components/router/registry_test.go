package router_test

import (
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/router"
	"github.com/farazhassan/gantry/eval"
)

// newTestAgent builds a minimal agent whose LLM is never invoked in
// registry/classifier tests. Shared by every test file in this package.
func newTestAgent(t *testing.T) *gantry.Agent {
	t.Helper()
	a, err := gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient()))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	return a
}

func TestRegistryAddGetKeys(t *testing.T) {
	reg := router.NewRegistry()
	billing := newTestAgent(t)
	support := newTestAgent(t)

	if err := reg.Add("billing", "Invoices and refunds.", billing); err != nil {
		t.Fatalf("Add billing: %v", err)
	}
	if err := reg.Add("support", "Product help.", support); err != nil {
		t.Fatalf("Add support: %v", err)
	}

	got, ok := reg.Get("billing")
	if !ok || got != billing {
		t.Errorf("Get(billing) = (%p, %v), want (%p, true)", got, ok, billing)
	}
	if _, ok := reg.Get("missing"); ok {
		t.Errorf("Get(missing) = (_, true), want false")
	}
	keys := reg.Keys()
	if len(keys) != 2 || keys[0] != "billing" || keys[1] != "support" {
		t.Errorf("Keys() = %v, want [billing support] (insertion order)", keys)
	}
	desc, ok := reg.Description("support")
	if !ok || desc != "Product help." {
		t.Errorf("Description(support) = (%q, %v), want (\"Product help.\", true)", desc, ok)
	}
}

func TestRegistryRejectsDuplicateKey(t *testing.T) {
	reg := router.NewRegistry()
	if err := reg.Add("billing", "first", newTestAgent(t)); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	err := reg.Add("billing", "second", newTestAgent(t))
	if err == nil {
		t.Fatalf("duplicate Add = nil error, want an error")
	}
	if !strings.Contains(err.Error(), "billing") {
		t.Errorf("error %q does not name the duplicate key", err)
	}
	// The original registration is untouched.
	if desc, _ := reg.Description("billing"); desc != "first" {
		t.Errorf("Description = %q, want first (original untouched)", desc)
	}
	if keys := reg.Keys(); len(keys) != 1 {
		t.Errorf("Keys() = %v, want exactly one entry", keys)
	}
}

func TestRegistryRejectsEmptyKeyAndNilAgent(t *testing.T) {
	reg := router.NewRegistry()
	if err := reg.Add("", "desc", newTestAgent(t)); err == nil {
		t.Errorf("Add with empty key = nil error, want an error")
	}
	if err := reg.Add("billing", "desc", nil); err == nil {
		t.Errorf("Add with nil agent = nil error, want an error")
	}
	if keys := reg.Keys(); len(keys) != 0 {
		t.Errorf("Keys() = %v, want empty after rejected Adds", keys)
	}
}

func TestRegistryKeysIsACopy(t *testing.T) {
	reg := router.NewRegistry()
	if err := reg.Add("billing", "d", newTestAgent(t)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	keys := reg.Keys()
	keys[0] = "mutated"
	if got := reg.Keys(); got[0] != "billing" {
		t.Errorf("Keys()[0] = %q after caller mutation, want billing (copy)", got[0])
	}
}

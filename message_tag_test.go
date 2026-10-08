package gantry_test

import (
	"encoding/json"
	"testing"

	"github.com/farazhassan/gantry"
)

func TestMessageTagIsPrivate(t *testing.T) {
	m := gantry.WithTag(gantry.Message{Role: gantry.RoleUser, Content: "x"}, "t")
	if gantry.Tag(m) != "t" {
		t.Fatalf("Tag = %q, want t", gantry.Tag(m))
	}
	copied := m
	if gantry.Tag(copied) != "t" {
		t.Error("tag lost on copy")
	}
	b, _ := json.Marshal(m)
	var back gantry.Message
	_ = json.Unmarshal(b, &back)
	if gantry.Tag(back) != "" {
		t.Errorf("tag survived JSON: %s", b)
	}
}

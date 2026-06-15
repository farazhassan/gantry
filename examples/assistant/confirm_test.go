package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/farazhassan/gantry/components/humanloop"
)

func TestIsReadOnly(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"fs__read_file", true},
		{"fs__list_directory", true},
		{"fs__write_file", false},
		{"fs__edit_file", false},
		{"fs__some_new_tool", false}, // unknown fs tool -> fail-safe (mutating)
		{"web__fetch", true},
		{"time__get_current_time", true},
		{"ask_user", true},
		{"totally_unknown", false}, // unknown namespace -> fail-safe
	}
	for _, c := range cases {
		if got := isReadOnly(c.name); got != c.want {
			t.Errorf("isReadOnly(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCLIConfirmer_AutoAllowsReadOnly(t *testing.T) {
	var out strings.Builder
	c := newCLIConfirmer(strings.NewReader(""), &out)
	d, err := c.Confirm(context.Background(), humanloop.Action{Kind: "tool", Name: "fs__read_file", Args: json.RawMessage(`{"path":"/tmp/x"}`)})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if !d.Approved {
		t.Fatalf("read-only tool must be auto-approved")
	}
}

func TestCLIConfirmer_PromptsAndApprovesOnYes(t *testing.T) {
	var out strings.Builder
	c := newCLIConfirmer(strings.NewReader("y\n"), &out)
	d, err := c.Confirm(context.Background(), humanloop.Action{Kind: "tool", Name: "fs__write_file", Args: json.RawMessage(`{"path":"/tmp/x","content":"hi"}`)})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if !d.Approved {
		t.Fatalf("expected approval on 'y'")
	}
	if !strings.Contains(out.String(), "fs__write_file") {
		t.Fatalf("prompt should name the tool, got: %q", out.String())
	}
}

func TestCLIConfirmer_PromptsAndDeniesOnNo(t *testing.T) {
	var out strings.Builder
	c := newCLIConfirmer(strings.NewReader("n\n"), &out)
	d, err := c.Confirm(context.Background(), humanloop.Action{Kind: "tool", Name: "fs__write_file", Args: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if d.Approved {
		t.Fatalf("expected denial on 'n'")
	}
}

func TestCLIConfirmer_DeniesOnEOF(t *testing.T) {
	var out strings.Builder
	c := newCLIConfirmer(strings.NewReader(""), &out) // no answer -> EOF
	d, err := c.Confirm(context.Background(), humanloop.Action{Kind: "tool", Name: "fs__write_file", Args: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if d.Approved {
		t.Fatalf("EOF on a mutating prompt must default to deny")
	}
}

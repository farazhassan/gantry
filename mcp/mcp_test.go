package mcp

import (
	"context"
	"testing"
)

func TestToolsDiscoversAll(t *testing.T) {
	session := newTestSession(t)
	tools, err := Tools(context.Background(), session)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 3 {
		t.Fatalf("discovered %d tools, want 3", len(tools))
	}
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Definition().Name] = true
	}
	for _, want := range []string{"echo", "snapshot", "fail"} {
		if !names[want] {
			t.Fatalf("missing tool %q (got %v)", want, names)
		}
	}
}

func TestToolsInvokeSnapshotImagePlaceholder(t *testing.T) {
	session := newTestSession(t)
	tools, err := Tools(context.Background(), session)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	snap := findTool(t, tools, "snapshot")
	out, err := snap.Invoke(context.Background(), nil)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	var got string
	if err := jsonUnmarshal(out, &got); err != nil {
		t.Fatalf("output not a JSON string: %v", err)
	}
	if got != "[image: image/png omitted]" {
		t.Fatalf("snapshot result = %q", got)
	}
}

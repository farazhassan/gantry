package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/farazhassan/gantry/components/tool"
)

func findTool(t *testing.T, tools []tool.Tool, name string) tool.Tool {
	t.Helper()
	for _, tl := range tools {
		if tl.Definition().Name == name {
			return tl
		}
	}
	t.Fatalf("tool %q not found in %d tools", name, len(tools))
	return nil
}

func TestMCPToolDefinition(t *testing.T) {
	session := newTestSession(t)
	tools, err := Tools(context.Background(), session)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	echo := findTool(t, tools, "echo")
	def := echo.Definition()
	if def.Description != "echoes the message" {
		t.Fatalf("description = %q", def.Description)
	}
	var schema map[string]any
	if err := json.Unmarshal(def.Schema, &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v (raw=%s)", err, def.Schema)
	}
	if schema["type"] != "object" {
		t.Fatalf("schema type = %v, want object", schema["type"])
	}
}

func TestMCPToolInvokeText(t *testing.T) {
	session := newTestSession(t)
	tools, err := Tools(context.Background(), session)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	echo := findTool(t, tools, "echo")
	out, err := echo.Invoke(context.Background(), json.RawMessage(`{"msg":"hi there"}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	var got string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output not a JSON string: %v", err)
	}
	if got != "hi there" {
		t.Fatalf("Invoke = %q, want %q", got, "hi there")
	}
}

func TestMCPToolInvokeError(t *testing.T) {
	session := newTestSession(t)
	tools, err := Tools(context.Background(), session)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	fail := findTool(t, tools, "fail")
	_, err = fail.Invoke(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("Invoke: want error from IsError result, got nil")
	}
}

func TestMCPToolNamespace(t *testing.T) {
	session := newTestSession(t)
	tools, err := Tools(context.Background(), session, WithNamespace("fs"))
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	echo := findTool(t, tools, "fs__echo")
	out, err := echo.Invoke(context.Background(), json.RawMessage(`{"msg":"x"}`))
	if err != nil {
		t.Fatalf("Invoke with namespace: %v", err)
	}
	var got string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output not a JSON string: %v", err)
	}
	if got != "x" {
		t.Fatalf("Invoke = %q, want %q", got, "x")
	}
}

package mcp

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMapResult(t *testing.T) {
	tests := []struct {
		name    string
		res     *mcp.CallToolResult
		want    string // decoded JSON string; "" means expect an error
		wantErr bool
	}{
		{
			name: "single text block",
			res:  &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "hello"}}},
			want: "hello",
		},
		{
			name: "joins multiple text blocks with newline",
			res: &mcp.CallToolResult{Content: []mcp.Content{
				&mcp.TextContent{Text: "a"},
				&mcp.TextContent{Text: "b"},
			}},
			want: "a\nb",
		},
		{
			name: "image becomes placeholder",
			res:  &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{MIMEType: "image/png"}}},
			want: "[image: image/png omitted]",
		},
		{
			name: "text and image mixed",
			res: &mcp.CallToolResult{Content: []mcp.Content{
				&mcp.TextContent{Text: "see"},
				&mcp.ImageContent{MIMEType: "image/jpeg"},
			}},
			want: "see\n[image: image/jpeg omitted]",
		},
		{
			name: "empty content is empty string",
			res:  &mcp.CallToolResult{Content: nil},
			want: "",
		},
		{
			name:    "IsError returns a Go error",
			res:     &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "boom"}}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mapResult("toolx", tt.res)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("mapResult: want error, got nil (out=%s)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("mapResult: unexpected error: %v", err)
			}
			var decoded string
			if err := jsonUnmarshal(got, &decoded); err != nil {
				t.Fatalf("result is not a JSON string: %v (raw=%s)", err, got)
			}
			if decoded != tt.want {
				t.Fatalf("mapResult = %q, want %q", decoded, tt.want)
			}
		})
	}
}

func TestMapResultErrorIncludesText(t *testing.T) {
	res := &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "disk full"}}}
	_, err := mapResult("writer", res)
	if err == nil {
		t.Fatal("want error")
	}
	if got := err.Error(); !strings.Contains(got, "disk full") || !strings.Contains(got, "writer") {
		t.Fatalf("error %q should mention tool name and server message", got)
	}
}

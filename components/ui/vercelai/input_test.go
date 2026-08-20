package vercelai

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/farazhassan/gantry"
)

func TestChatRequestDecode(t *testing.T) {
	body := `{"id":"chat1","trigger":"submit-message","messages":[
		{"id":"msg1","role":"user","parts":[{"type":"text","text":"hi"}]}
	]}`
	var r ChatRequest
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if r.ID != "chat1" || r.Trigger != "submit-message" {
		t.Fatalf("got ID=%q Trigger=%q", r.ID, r.Trigger)
	}
	if len(r.Messages) != 1 || r.Messages[0].Role != "user" || r.Messages[0].Parts[0].Text != "hi" {
		t.Fatalf("got %#v", r.Messages)
	}
}

func TestUserTextConcatenatesTextParts(t *testing.T) {
	m := UIMessage{Role: "user", Parts: []Part{
		{Type: "text", Text: "hello "},
		{Type: "step-start"},
		{Type: "text", Text: "world"},
	}}
	got, err := userText(m)
	if err != nil {
		t.Fatalf("userText: %v", err)
	}
	if got != "hello world" {
		t.Fatalf("got %q, want %q", got, "hello world")
	}
}

func TestUserTextSkipsMetadataParts(t *testing.T) {
	m := UIMessage{Role: "user", Parts: []Part{
		{Type: "text", Text: "hi"},
		{Type: "reasoning", Text: "should not appear"},
		{Type: "source-url"},
		{Type: "data-mydata"},
	}}
	got, err := userText(m)
	if err != nil {
		t.Fatalf("userText: %v", err)
	}
	if got != "hi" {
		t.Fatalf("got %q, want %q", got, "hi")
	}
}

func TestUserTextErrorsOnFilePart(t *testing.T) {
	m := UIMessage{Role: "user", Parts: []Part{{Type: "file"}}}
	if _, err := userText(m); err == nil {
		t.Fatal("expected an error for a file part in a user message")
	}
}

func TestUserTextErrorsOnToolPart(t *testing.T) {
	m := UIMessage{Role: "user", Parts: []Part{{Type: "dynamic-tool", ToolCallID: "c1"}}}
	if _, err := userText(m); err == nil {
		t.Fatal("expected an error for a tool part in a user message")
	}
}

func TestUserTextErrorsOnUnrecognizedPart(t *testing.T) {
	m := UIMessage{Role: "user", Parts: []Part{{Type: "some-future-part"}}}
	if _, err := userText(m); err == nil {
		t.Fatal("expected an error for an unrecognized part type")
	}
}

func TestToGantryMessagesSimpleAssistant(t *testing.T) {
	m := UIMessage{Role: "assistant", Parts: []Part{{Type: "text", Text: "hi there"}}}
	got, err := toGantryMessages(m)
	if err != nil {
		t.Fatalf("toGantryMessages: %v", err)
	}
	want := []gantry.Message{{Role: gantry.RoleAssistant, Content: "hi there"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestToGantryMessagesAssistantWithToolCallAndResult(t *testing.T) {
	m := UIMessage{Role: "assistant", Parts: []Part{
		{Type: "text", Text: "let me check"},
		{Type: "dynamic-tool", ToolCallID: "c1", ToolName: "search", State: "output-available", Input: json.RawMessage(`{"q":"x"}`), Output: json.RawMessage(`"found it"`)},
	}}
	got, err := toGantryMessages(m)
	if err != nil {
		t.Fatalf("toGantryMessages: %v", err)
	}
	want := []gantry.Message{
		{Role: gantry.RoleAssistant, Content: "let me check", ToolCalls: []gantry.ToolCall{{ID: "c1", Name: "search", Input: json.RawMessage(`{"q":"x"}`)}}},
		{Role: gantry.RoleTool, ToolCallID: "c1", Content: `"found it"`},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestToGantryMessagesSegmentsOnStepStart(t *testing.T) {
	// One UIMessage spanning two agentic-loop steps: a tool round, then a
	// step-start boundary, then the final text-only reply. This must become
	// TWO gantry.Message turns (plus the tool result), not one.
	m := UIMessage{Role: "assistant", Parts: []Part{
		{Type: "dynamic-tool", ToolCallID: "c1", ToolName: "search", State: "output-available", Input: json.RawMessage(`{}`), Output: json.RawMessage(`"ok"`)},
		{Type: "step-start"},
		{Type: "text", Text: "Here's what I found."},
	}}
	got, err := toGantryMessages(m)
	if err != nil {
		t.Fatalf("toGantryMessages: %v", err)
	}
	want := []gantry.Message{
		{Role: gantry.RoleAssistant, ToolCalls: []gantry.ToolCall{{ID: "c1", Name: "search", Input: json.RawMessage(`{}`)}}},
		{Role: gantry.RoleTool, ToolCallID: "c1", Content: `"ok"`},
		{Role: gantry.RoleAssistant, Content: "Here's what I found."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestToGantryMessagesUnresolvedToolCallHasNoResult(t *testing.T) {
	m := UIMessage{Role: "assistant", Parts: []Part{
		{Type: "dynamic-tool", ToolCallID: "c1", ToolName: "ask_user", State: "input-available", Input: json.RawMessage(`{}`)},
	}}
	got, err := toGantryMessages(m)
	if err != nil {
		t.Fatalf("toGantryMessages: %v", err)
	}
	want := []gantry.Message{{Role: gantry.RoleAssistant, ToolCalls: []gantry.ToolCall{{ID: "c1", Name: "ask_user", Input: json.RawMessage(`{}`)}}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestToGantryMessagesUnknownRole(t *testing.T) {
	if _, err := toGantryMessages(UIMessage{Role: "developer"}); err == nil {
		t.Fatal("expected an error for an unknown role")
	}
}

func TestRequireToolResultsWellFormed(t *testing.T) {
	msgs := []gantry.Message{
		{Role: gantry.RoleUser, Content: "hi"},
		{Role: gantry.RoleAssistant, ToolCalls: []gantry.ToolCall{{ID: "c1", Name: "x"}}},
		{Role: gantry.RoleTool, ToolCallID: "c1", Content: "ok"},
	}
	if err := requireToolResults(msgs); err != nil {
		t.Fatalf("requireToolResults: %v", err)
	}
}

func TestRequireToolResultsUnansweredCall(t *testing.T) {
	msgs := []gantry.Message{
		{Role: gantry.RoleAssistant, ToolCalls: []gantry.ToolCall{{ID: "c1", Name: "x"}}},
	}
	if err := requireToolResults(msgs); err == nil {
		t.Fatal("expected an error for an unanswered tool call")
	}
}

func TestRequireToolResultsDuplicateID(t *testing.T) {
	msgs := []gantry.Message{
		{Role: gantry.RoleAssistant, ToolCalls: []gantry.ToolCall{{ID: "c1", Name: "x"}}},
		{Role: gantry.RoleTool, ToolCallID: "c1", Content: "ok"},
		{Role: gantry.RoleAssistant, ToolCalls: []gantry.ToolCall{{ID: "c1", Name: "x"}}},
	}
	if err := requireToolResults(msgs); err == nil {
		t.Fatal("expected an error for a duplicate tool call id")
	}
}

func TestRequireToolResultsOrphanResult(t *testing.T) {
	msgs := []gantry.Message{{Role: gantry.RoleTool, ToolCallID: "c1", Content: "ok"}}
	if err := requireToolResults(msgs); err == nil {
		t.Fatal("expected an error for a tool result with no preceding call")
	}
}

func TestToRunHappyPath(t *testing.T) {
	r := &ChatRequest{Messages: []UIMessage{
		{Role: "user", Parts: []Part{{Type: "text", Text: "hello"}}},
		{Role: "assistant", Parts: []Part{{Type: "text", Text: "hi there"}}},
		{Role: "user", Parts: []Part{{Type: "text", Text: "how are you"}}},
	}}
	prior, input, err := r.ToRun()
	if err != nil {
		t.Fatalf("ToRun: %v", err)
	}
	if input != "how are you" {
		t.Fatalf("input = %q, want %q", input, "how are you")
	}
	want := []gantry.Message{
		{Role: gantry.RoleUser, Content: "hello"},
		{Role: gantry.RoleAssistant, Content: "hi there"},
	}
	if !reflect.DeepEqual(prior.Messages, want) {
		t.Fatalf("got  %#v\nwant %#v", prior.Messages, want)
	}
}

func TestToRunEmptyMessages(t *testing.T) {
	r := &ChatRequest{}
	if _, _, err := r.ToRun(); err == nil {
		t.Fatal("expected an error for empty Messages")
	}
}

func TestToRunLastMessageNotUser(t *testing.T) {
	r := &ChatRequest{Messages: []UIMessage{{Role: "assistant", Parts: []Part{{Type: "text", Text: "hi"}}}}}
	if _, _, err := r.ToRun(); err == nil {
		t.Fatal("expected an error when the last message is not role user")
	}
}

func TestToRunRejectsUnansweredHistoryToolCall(t *testing.T) {
	r := &ChatRequest{Messages: []UIMessage{
		{Role: "assistant", Parts: []Part{{Type: "dynamic-tool", ToolCallID: "c1", ToolName: "ask_user", State: "input-available", Input: json.RawMessage(`{}`)}}},
		{Role: "user", Parts: []Part{{Type: "text", Text: "hi"}}},
	}}
	if _, _, err := r.ToRun(); err == nil {
		t.Fatal("expected an error: c1 in history is never answered")
	}
}

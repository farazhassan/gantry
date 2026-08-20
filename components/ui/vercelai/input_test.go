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

func TestToResumeHappyPath(t *testing.T) {
	r := &ChatRequest{Messages: []UIMessage{
		{Role: "user", Parts: []Part{{Type: "text", Text: "hi, I am Ada"}}},
		{Role: "assistant", Parts: []Part{
			{Type: "dynamic-tool", ToolCallID: "q1", ToolName: "ask_user", State: "output-available",
				Input: json.RawMessage(`{"q":"name?"}`), Output: json.RawMessage(`{"answer":"Ada"}`)},
		}},
	}}
	prior, err := r.ToResume()
	if err != nil {
		t.Fatalf("ToResume: %v", err)
	}
	want := []gantry.Message{
		{Role: gantry.RoleUser, Content: "hi, I am Ada"},
		{Role: gantry.RoleAssistant, ToolCalls: []gantry.ToolCall{{ID: "q1", Name: "ask_user", Input: json.RawMessage(`{"q":"name?"}`)}}},
		{Role: gantry.RoleTool, ToolCallID: "q1", Content: `{"answer":"Ada"}`},
	}
	if !reflect.DeepEqual(prior.Messages, want) {
		t.Fatalf("got  %#v\nwant %#v", prior.Messages, want)
	}
	if prior.Meta == nil {
		t.Error("Meta is nil, want an initialized map (needed by the client-tools advertise middleware)")
	}
	if prior.Trace == nil {
		t.Error("Trace is nil, want gantry.NewTrace()")
	}
}

func TestToResumeEmptyMessages(t *testing.T) {
	r := &ChatRequest{}
	if _, err := r.ToResume(); err == nil {
		t.Fatal("expected an error for empty Messages")
	}
}

func TestToResumeLastMessageHasNoToolPart(t *testing.T) {
	r := &ChatRequest{Messages: []UIMessage{
		{Role: "assistant", Parts: []Part{{Type: "text", Text: "just text, nothing to resume"}}},
	}}
	if _, err := r.ToResume(); err == nil {
		t.Fatal("expected an error when the last message has no tool part")
	}
}

func TestToResumeRejectsUnresolvedLastToolCall(t *testing.T) {
	r := &ChatRequest{Messages: []UIMessage{
		{Role: "assistant", Parts: []Part{
			{Type: "dynamic-tool", ToolCallID: "q1", ToolName: "ask_user", State: "input-available", Input: json.RawMessage(`{}`)},
		}},
	}}
	if _, err := r.ToResume(); err == nil {
		t.Fatal("expected an error: q1 in the last message is never answered")
	}
}

func TestToResumeRejectsIncompleteMultiCall(t *testing.T) {
	// Mirrors agui's TestHandlerRejectsIncompleteResume: two tool calls in
	// the last message, only one answered.
	r := &ChatRequest{Messages: []UIMessage{
		{Role: "assistant", Parts: []Part{
			{Type: "dynamic-tool", ToolCallID: "q1", ToolName: "ask_user", State: "output-available", Input: json.RawMessage(`{}`), Output: json.RawMessage(`{}`)},
			{Type: "dynamic-tool", ToolCallID: "q2", ToolName: "ask_user", State: "input-available", Input: json.RawMessage(`{}`)},
		}},
	}}
	if _, err := r.ToResume(); err == nil {
		t.Fatal("expected an error: q2 has no result")
	}
}

func TestHasToolPart(t *testing.T) {
	if hasToolPart(UIMessage{Parts: []Part{{Type: "text"}}}) {
		t.Error("hasToolPart(text-only) = true, want false")
	}
	if !hasToolPart(UIMessage{Parts: []Part{{Type: "dynamic-tool"}}}) {
		t.Error("hasToolPart(dynamic-tool) = false, want true")
	}
	if !hasToolPart(UIMessage{Parts: []Part{{Type: "tool-search"}}}) {
		t.Error("hasToolPart(tool-search) = false, want true")
	}
}

func TestHasToolPartIgnoresEarlierResolvedSegment(t *testing.T) {
	m := UIMessage{Role: "assistant", Parts: []Part{
		{Type: "dynamic-tool", ToolCallID: "c1", ToolName: "search", State: "output-available", Input: json.RawMessage(`{}`), Output: json.RawMessage(`"a"`)},
		{Type: "step-start"},
		{Type: "text", Text: "done"},
	}}
	if hasToolPart(m) {
		t.Error("hasToolPart = true, want false: the final segment has no tool part, only an earlier resolved one")
	}
}

func TestToResumeRejectsAlreadyCompletedMultiStepMessage(t *testing.T) {
	r := &ChatRequest{Messages: []UIMessage{
		{Role: "user", Parts: []Part{{Type: "text", Text: "hi"}}},
		{Role: "assistant", Parts: []Part{
			{Type: "dynamic-tool", ToolCallID: "c1", ToolName: "search", State: "output-available", Input: json.RawMessage(`{}`), Output: json.RawMessage(`"a"`)},
			{Type: "step-start"},
			{Type: "text", Text: "done"},
		}},
	}}
	if _, err := r.ToResume(); err == nil {
		t.Fatal("expected an error: the final segment is already complete, nothing to resume")
	}
}

func TestSegmentAssistantConsecutiveStepStartsProduceNoSpuriousMessage(t *testing.T) {
	m := UIMessage{Role: "assistant", Parts: []Part{
		{Type: "text", Text: "first"},
		{Type: "step-start"},
		{Type: "step-start"},
		{Type: "text", Text: "second"},
	}}
	got, err := toGantryMessages(m)
	if err != nil {
		t.Fatalf("toGantryMessages: %v", err)
	}
	want := []gantry.Message{
		{Role: gantry.RoleAssistant, Content: "first"},
		{Role: gantry.RoleAssistant, Content: "second"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestSegmentAssistantTwoToolCallsOneResolvedOneNot(t *testing.T) {
	m := UIMessage{Role: "assistant", Parts: []Part{
		{Type: "dynamic-tool", ToolCallID: "c1", ToolName: "search", State: "output-available", Input: json.RawMessage(`{}`), Output: json.RawMessage(`"a"`)},
		{Type: "dynamic-tool", ToolCallID: "c2", ToolName: "ask_user", State: "input-available", Input: json.RawMessage(`{}`)},
	}}
	got, err := toGantryMessages(m)
	if err != nil {
		t.Fatalf("toGantryMessages: %v", err)
	}
	want := []gantry.Message{
		{
			Role: gantry.RoleAssistant,
			ToolCalls: []gantry.ToolCall{
				{ID: "c1", Name: "search", Input: json.RawMessage(`{}`)},
				{ID: "c2", Name: "ask_user", Input: json.RawMessage(`{}`)},
			},
		},
		{Role: gantry.RoleTool, ToolCallID: "c1", Content: `"a"`},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestSegmentAssistantUnrecognizedPartTypeErrors(t *testing.T) {
	m := UIMessage{Role: "assistant", Parts: []Part{{Type: "some-future-part"}}}
	if _, err := toGantryMessages(m); err == nil {
		t.Fatal("expected an error for an unrecognized part type in an assistant message")
	}
}

func TestSegmentAssistantFilePartSkipped(t *testing.T) {
	m := UIMessage{Role: "assistant", Parts: []Part{
		{Type: "text", Text: "here's an image"},
		{Type: "file"},
	}}
	got, err := toGantryMessages(m)
	if err != nil {
		t.Fatalf("toGantryMessages: %v", err)
	}
	want := []gantry.Message{{Role: gantry.RoleAssistant, Content: "here's an image"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestToolCallAndResultErrorBranches(t *testing.T) {
	if _, _, err := toolCallAndResult(Part{ToolName: "x", State: "output-available"}); err == nil {
		t.Error("expected an error for missing toolCallId")
	}
	if _, _, err := toolCallAndResult(Part{ToolCallID: "c1", State: "output-available"}); err == nil {
		t.Error("expected an error for missing toolName")
	}
	if _, _, err := toolCallAndResult(Part{ToolCallID: "c1", ToolName: "x", Input: json.RawMessage(`{not valid json`), State: "output-available"}); err == nil {
		t.Error("expected an error for invalid JSON input")
	}
	if _, _, err := toolCallAndResult(Part{ToolCallID: "c1", ToolName: "x", State: "some-unknown-state"}); err == nil {
		t.Error("expected an error for an unrecognized state")
	}
}

func TestToolCallAndResultOutputErrorAndDenied(t *testing.T) {
	_, resErr, err := toolCallAndResult(Part{ToolCallID: "c1", ToolName: "x", State: "output-error", ErrorText: "boom"})
	if err != nil {
		t.Fatalf("toolCallAndResult: %v", err)
	}
	if resErr == nil || resErr.Content != "boom" {
		t.Fatalf("output-error result = %#v, want Content \"boom\"", resErr)
	}

	_, resDenied, err := toolCallAndResult(Part{ToolCallID: "c2", ToolName: "x", State: "output-denied"})
	if err != nil {
		t.Fatalf("toolCallAndResult: %v", err)
	}
	if resDenied == nil || resDenied.Content == "" {
		t.Fatalf("output-denied result = %#v, want a non-empty denial message", resDenied)
	}
}

func TestToGantryMessagesSystemRole(t *testing.T) {
	m := UIMessage{Role: "system", Parts: []Part{{Type: "text", Text: "be helpful"}}}
	got, err := toGantryMessages(m)
	if err != nil {
		t.Fatalf("toGantryMessages: %v", err)
	}
	want := []gantry.Message{{Role: gantry.RoleSystem, Content: "be helpful"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

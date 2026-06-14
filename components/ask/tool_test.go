// components/ask/tool_test.go
package ask_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/farazhassan/gantry/components/ask"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/conformance"
)

func TestDefinitionDefaultNameAndValidSchema(t *testing.T) {
	tl := ask.NewTool(ask.NewAuto(ask.Response{}))
	def := tl.Definition()
	if def.Name != "ask_user" {
		t.Errorf("Name = %q, want ask_user", def.Name)
	}
	if def.Description == "" {
		t.Errorf("Description is empty")
	}
	var v any
	if err := json.Unmarshal(def.Schema, &v); err != nil {
		t.Errorf("Schema is not valid JSON: %v", err)
	}
}

func TestWithNameOverridesToolName(t *testing.T) {
	tl := ask.NewTool(ask.NewAuto(ask.Response{}), ask.WithName("clarify"))
	if got := tl.Definition().Name; got != "clarify" {
		t.Errorf("Name = %q, want clarify", got)
	}
}

func TestNewToolPanicsOnNilPrompter(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Errorf("expected panic on nil Prompter")
		}
	}()
	_ = ask.NewTool(nil)
}


func TestAskUserConformsToToolSuite(t *testing.T) {
	conformance.ToolSuite(t, func() tool.Tool {
		return ask.NewTool(ask.NewAuto(ask.Response{Answers: []ask.Answer{}}))
	})
}

func TestInvokeHappyPathReturnsResponseJSON(t *testing.T) {
	var gotReq ask.Request
	p := ask.PrompterFunc(func(_ context.Context, req ask.Request) (ask.Response, error) {
		gotReq = req
		return ask.Response{Answers: []ask.Answer{
			{Header: "color", Status: ask.StatusAnswered, Values: []string{"red"}},
			{Header: "free", Status: ask.StatusAnswered, Values: []string{"typed text"}},
		}}, nil
	})
	tl := ask.NewTool(p)
	in := json.RawMessage(`{"questions":[
		{"header":"color","text":"pick","options":["red","blue"]},
		{"header":"free","text":"say"}
	]}`)
	out, err := tl.Invoke(context.Background(), in)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if len(gotReq.Questions) != 2 || gotReq.Questions[0].Options[0] != "red" {
		t.Fatalf("request not mapped: %+v", gotReq)
	}
	var resp ask.Response
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("output not JSON Response: %v", err)
	}
	if resp.Answers[0].Header != "color" || resp.Answers[1].Values[0] != "typed text" {
		t.Fatalf("unexpected output: %+v", resp)
	}
}

func TestInvokeTriStateRoundTrip(t *testing.T) {
	p := ask.NewAuto(ask.Response{Answers: []ask.Answer{
		{Header: "a", Status: ask.StatusDeclined},
		{Header: "b", Status: ask.StatusCancelled},
	}})
	tl := ask.NewTool(p)
	out, err := tl.Invoke(context.Background(), json.RawMessage(`{"questions":[
		{"header":"a","text":"x"},{"header":"b","text":"y"}
	]}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	var resp ask.Response
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Answers[0].Status != ask.StatusDeclined || resp.Answers[1].Status != ask.StatusCancelled {
		t.Fatalf("tri-state not preserved: %+v", resp)
	}
}

func TestInvokeValidationErrors(t *testing.T) {
	tl := ask.NewTool(ask.NewAuto(ask.Response{}))
	cases := map[string]string{
		"no questions":        `{"questions":[]}`,
		"too many questions":  `{"questions":[{"header":"a","text":"t"},{"header":"b","text":"t"},{"header":"c","text":"t"},{"header":"d","text":"t"},{"header":"e","text":"t"}]}`,
		"missing header":      `{"questions":[{"text":"t"}]}`,
		"long header":         `{"questions":[{"header":"thisistoolong!","text":"t"}]}`,
		"missing text":        `{"questions":[{"header":"h"}]}`,
		"one option":          `{"questions":[{"header":"h","text":"t","options":["only"]}]}`,
		"five options":        `{"questions":[{"header":"h","text":"t","options":["1","2","3","4","5"]}]}`,
		"multiselect no opts": `{"questions":[{"header":"h","text":"t","multiSelect":true}]}`,
		"malformed json":      `{"questions":`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := tl.Invoke(context.Background(), json.RawMessage(in))
			if err == nil {
				t.Errorf("expected error for %s", name)
			}
		})
	}
}

func TestInvokeDoesNotCallPrompterOnValidationError(t *testing.T) {
	called := false
	tl := ask.NewTool(ask.PrompterFunc(func(context.Context, ask.Request) (ask.Response, error) {
		called = true
		return ask.Response{}, nil
	}))
	_, _ = tl.Invoke(context.Background(), json.RawMessage(`{"questions":[]}`))
	if called {
		t.Errorf("Prompter must not be called when validation fails")
	}
}

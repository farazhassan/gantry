// components/ask/prompter_test.go
package ask_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/farazhassan/gantry/components/ask"
)

func TestPrompterFuncAdaptsToInterface(t *testing.T) {
	var p ask.Prompter = ask.PrompterFunc(func(_ context.Context, req ask.Request) (ask.Response, error) {
		return ask.Response{Answers: []ask.Answer{{
			Header: req.Questions[0].Header,
			Status: ask.StatusAnswered,
			Values: []string{"ok"},
		}}}, nil
	})
	resp, err := p.Prompt(context.Background(), ask.Request{
		Questions: []ask.Question{{Header: "h", Text: "t"}},
	})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if len(resp.Answers) != 1 || resp.Answers[0].Status != ask.StatusAnswered {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestResponseJSONRoundTrip(t *testing.T) {
	in := ask.Response{Answers: []ask.Answer{
		{Header: "color", Status: ask.StatusAnswered, Values: []string{"red", "blue"}},
		{Header: "name", Status: ask.StatusDeclined},
	}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out ask.Response
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Answers[0].Values[1] != "blue" || out.Answers[1].Status != ask.StatusDeclined {
		t.Fatalf("round trip mismatch: %+v", out)
	}
}

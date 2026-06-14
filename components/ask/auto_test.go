// components/ask/auto_test.go
package ask_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/components/ask"
)

func TestAutoReturnsCannedResponse(t *testing.T) {
	want := ask.Response{Answers: []ask.Answer{
		{Header: "ok", Status: ask.StatusAnswered, Values: []string{"yes"}},
	}}
	a := ask.NewAuto(want)
	got, err := a.Prompt(context.Background(), ask.Request{
		Questions: []ask.Question{{Header: "ok", Text: "proceed?", Options: []string{"yes", "no"}}},
	})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if len(got.Answers) != 1 || got.Answers[0].Values[0] != "yes" {
		t.Fatalf("unexpected: %+v", got)
	}
}

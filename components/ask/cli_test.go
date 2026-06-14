// components/ask/cli_test.go
package ask_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry/components/ask"
)

func prompt(t *testing.T, input string, qs ...ask.Question) ask.Response {
	t.Helper()
	c := ask.NewCLI(strings.NewReader(input), &bytes.Buffer{})
	resp, err := c.Prompt(context.Background(), ask.Request{Questions: qs})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	return resp
}

func TestCLISelectOneByNumber(t *testing.T) {
	resp := prompt(t, "2\n", ask.Question{Header: "color", Text: "pick", Options: []string{"red", "blue"}})
	if resp.Answers[0].Status != ask.StatusAnswered || resp.Answers[0].Values[0] != "blue" {
		t.Fatalf("got %+v", resp.Answers[0])
	}
}

func TestCLIMultiSelectByNumbers(t *testing.T) {
	resp := prompt(t, "1,3\n", ask.Question{Header: "langs", Text: "pick", Options: []string{"go", "rust", "c"}, MultiSelect: true})
	v := resp.Answers[0].Values
	if len(v) != 2 || v[0] != "go" || v[1] != "c" {
		t.Fatalf("got %+v", v)
	}
}

func TestCLISingleSelectIgnoresExtraNumbers(t *testing.T) {
	resp := prompt(t, "1,2\n", ask.Question{Header: "x", Text: "pick", Options: []string{"a", "b"}})
	if len(resp.Answers[0].Values) != 1 || resp.Answers[0].Values[0] != "a" {
		t.Fatalf("single-select should take first only: %+v", resp.Answers[0].Values)
	}
}

func TestCLIFreeTextNoOptions(t *testing.T) {
	resp := prompt(t, "hello world\n", ask.Question{Header: "name", Text: "say"})
	if resp.Answers[0].Values[0] != "hello world" {
		t.Fatalf("got %+v", resp.Answers[0])
	}
}

func TestCLICustomTextWhenOptionsPresent(t *testing.T) {
	resp := prompt(t, "magenta\n", ask.Question{Header: "color", Text: "pick", Options: []string{"red", "blue"}})
	if resp.Answers[0].Status != ask.StatusAnswered || resp.Answers[0].Values[0] != "magenta" {
		t.Fatalf("custom typed answer should pass through: %+v", resp.Answers[0])
	}
}

func TestCLISkipDeclines(t *testing.T) {
	resp := prompt(t, "/skip\n", ask.Question{Header: "name", Text: "say"})
	if resp.Answers[0].Status != ask.StatusDeclined {
		t.Fatalf("got %+v", resp.Answers[0])
	}
}

func TestCLIEOFCancelsAll(t *testing.T) {
	resp := prompt(t, "", // empty input -> immediate EOF
		ask.Question{Header: "a", Text: "x"},
		ask.Question{Header: "b", Text: "y"})
	for i, a := range resp.Answers {
		if a.Status != ask.StatusCancelled {
			t.Fatalf("answer %d not cancelled: %+v", i, a)
		}
	}
}

func TestCLIRendersHeaderTextAndOptions(t *testing.T) {
	out := &bytes.Buffer{}
	c := ask.NewCLI(strings.NewReader("1\n"), out)
	_, _ = c.Prompt(context.Background(), ask.Request{Questions: []ask.Question{
		{Header: "color", Text: "Pick a color", Options: []string{"red", "blue"}},
	}})
	s := out.String()
	if !strings.Contains(s, "color") || !strings.Contains(s, "Pick a color") ||
		!strings.Contains(s, "1) red") || !strings.Contains(s, "2) blue") {
		t.Fatalf("render missing expected content:\n%s", s)
	}
}

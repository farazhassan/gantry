package ask_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/ask"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/eval"
)

func TestValidateAcceptsValidRequest(t *testing.T) {
	in := `{"questions":[{"header":"Scope","text":"Which?","options":["a","b"],"multiSelect":true}]}`
	if err := ask.Validate(json.RawMessage(in)); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}
}

func TestValidateAcceptsLongHeader(t *testing.T) {
	in := `{"questions":[{"header":"Which revenue number should I use for the comparison?","text":"Which?"}]}`
	if err := ask.Validate(json.RawMessage(in)); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}
}

func TestValidateRejectsWithInvalidArgsSentinel(t *testing.T) {
	cases := map[string]string{
		"empty":               ``,
		"malformed json":      `{"questions":[{"header":"say "hi"","text":"t"}]}`,
		"no questions":        `{"questions":[]}`,
		"no header":           `{"questions":[{"header":"","text":"t"}]}`,
		"no text":             `{"questions":[{"header":"h","text":""}]}`,
		"multiselect no opts": `{"questions":[{"header":"h","text":"t","multiSelect":true}]}`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			err := ask.Validate(json.RawMessage(in))
			if !errors.Is(err, gantry.ErrToolInvalidArgs) {
				t.Fatalf("Validate = %v, want ErrToolInvalidArgs", err)
			}
		})
	}
}

func TestInvokeErrorMatchesValidate(t *testing.T) {
	in := json.RawMessage(`{"questions":[{"header":"h","text":""}]}`)
	_, invokeErr := ask.NewTool(ask.NewAuto(ask.Response{})).Invoke(context.Background(), in)
	if invokeErr == nil || invokeErr.Error() != ask.Validate(in).Error() {
		t.Fatalf("Invoke err = %v, want Validate's %v", invokeErr, ask.Validate(in))
	}
}

func TestClientValidationFeedsSchemaErrorsBack(t *testing.T) {
	noText := `{"questions":[{"header":"Scope","text":""}]}`
	valid := `{"questions":[{"header":"Scope","text":"Which?"}]}`
	for _, name := range []string{"ask_user", "clarify"} {
		t.Run(name, func(t *testing.T) {
			mock := eval.NewMockLLMClient(
				gantry.LLMResponse{
					ToolCalls:  []gantry.ToolCall{{ID: "q1", Name: name, Input: json.RawMessage(noText)}},
					StopReason: gantry.StopReasonToolUse,
				},
				gantry.LLMResponse{
					ToolCalls:  []gantry.ToolCall{{ID: "q2", Name: name, Input: json.RawMessage(valid)}},
					StopReason: gantry.StopReasonToolUse,
				},
			)
			a, _ := gantry.NewAgent(gantry.WithLLM(mock))
			opt := ask.WithName(name)
			if err := a.With(tool.Client(ask.Definition(opt)), ask.ClientValidation(opt)); err != nil {
				t.Fatalf("install: %v", err)
			}

			state, err := a.Run(context.Background(), "go")
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if state.DoneReason != gantry.DoneClientToolCall || len(state.PendingToolCalls) != 1 || state.PendingToolCalls[0].ID != "q2" {
				t.Fatalf("DoneReason=%q pending=%#v, want suspend on q2 only", state.DoneReason, state.PendingToolCalls)
			}
			want := ask.Validate(json.RawMessage(noText)).Error()
			for _, m := range state.Messages {
				if m.Role == gantry.RoleTool && m.ToolCallID == "q1" {
					if !m.IsError || m.Content != want {
						t.Fatalf("q1 result = %+v, want error %q", m, want)
					}
					return
				}
			}
			t.Fatalf("no tool result for rejected q1: %#v", state.Messages)
		})
	}
}

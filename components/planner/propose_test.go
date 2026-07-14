package planner

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
)

func TestProposePlanDefSchema(t *testing.T) {
	def := proposePlanDef()
	if def.Name != "propose_plan" {
		t.Errorf("Name = %q, want propose_plan", def.Name)
	}
	if def.Description == "" {
		t.Errorf("Description is empty")
	}
	var schema struct {
		Type       string `json:"type"`
		Properties struct {
			Steps struct {
				Type  string `json:"type"`
				Items struct {
					Properties map[string]json.RawMessage `json:"properties"`
					Required   []string                   `json:"required"`
				} `json:"items"`
			} `json:"steps"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(def.Schema, &schema); err != nil {
		t.Fatalf("Schema is not valid JSON: %v", err)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "steps" {
		t.Errorf("required = %v, want [steps]", schema.Required)
	}
	if schema.Properties.Steps.Type != "array" {
		t.Errorf("steps.type = %q, want array", schema.Properties.Steps.Type)
	}
	if _, ok := schema.Properties.Steps.Items.Properties["description"]; !ok {
		t.Errorf("step schema missing 'description' property")
	}
	if _, ok := schema.Properties.Steps.Items.Properties["acceptance_criteria"]; !ok {
		t.Errorf("step schema missing 'acceptance_criteria' property")
	}
	if req := schema.Properties.Steps.Items.Required; len(req) != 1 || req[0] != "description" {
		t.Errorf("step required = %v, want [description]", req)
	}
}

func TestParseProposedStepsJoinsCriteriaAndSkipsBlanks(t *testing.T) {
	resp := gantry.LLMResponse{ToolCalls: []gantry.ToolCall{{
		ID:   "c1",
		Name: "propose_plan",
		Input: json.RawMessage(`{"steps":[
			{"description":"design the API","acceptance_criteria":["endpoints documented","reviewed"]},
			{"description":"implement","acceptance_criteria":["all tests pass"]},
			{"description":"   ","acceptance_criteria":["ignored"]},
			{"description":"ship"}
		]}`),
	}}}
	steps, err := parseProposedSteps(resp)
	if err != nil {
		t.Fatalf("parseProposedSteps: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("got %d steps, want 3 (blank description dropped); steps = %+v", len(steps), steps)
	}
	if steps[0].Description != "design the API" || steps[0].AcceptanceCriteria != "endpoints documented; reviewed" {
		t.Errorf("step0 = %+v, want criteria joined with '; '", steps[0])
	}
	if steps[1].Description != "implement" || steps[1].AcceptanceCriteria != "all tests pass" {
		t.Errorf("step1 = %+v", steps[1])
	}
	if steps[2].Description != "ship" || steps[2].AcceptanceCriteria != "" {
		t.Errorf("step2 = %+v, want empty criteria", steps[2])
	}
	for i, s := range steps {
		if s.ID != "" {
			t.Errorf("step %d ID = %q, want empty (the task ledger mints IDs at adoption)", i, s.ID)
		}
	}
}

func TestParseProposedStepsMissingCallIsError(t *testing.T) {
	_, err := parseProposedSteps(gantry.LLMResponse{Content: "1. a text plan"})
	if err == nil {
		t.Fatal("parseProposedSteps = nil error, want missing-call error")
	}
	if !strings.Contains(err.Error(), "propose_plan") {
		t.Errorf("error = %v, want mention of propose_plan", err)
	}
}

func TestParseProposedStepsMalformedPayloadIsError(t *testing.T) {
	resp := gantry.LLMResponse{ToolCalls: []gantry.ToolCall{{
		ID: "c1", Name: "propose_plan", Input: json.RawMessage(`not json`),
	}}}
	if _, err := parseProposedSteps(resp); err == nil {
		t.Fatal("parseProposedSteps = nil error, want decode error")
	}
}

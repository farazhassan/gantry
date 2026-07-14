package planner

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
)

func ledgerPlan() *gantry.Plan {
	return &gantry.Plan{
		Goal: "ship it",
		Steps: []gantry.PlanStep{
			{ID: "s1", Description: "design", Status: gantry.StepActive, AcceptanceCriteria: "spec approved"},
			{ID: "s2", Description: "build", Status: gantry.StepPending, AcceptanceCriteria: "tests pass"},
		},
	}
}

func TestUpdatePlanToolDef(t *testing.T) {
	def := UpdatePlanToolDef()
	if def.Name != "update_plan" {
		t.Errorf("Name = %q, want update_plan", def.Name)
	}
	if def.Description == "" {
		t.Errorf("Description is empty")
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if err := json.Unmarshal(def.Schema, &schema); err != nil {
		t.Fatalf("Schema is not valid JSON: %v", err)
	}
	if _, ok := schema.Properties["ops"]; !ok {
		t.Errorf("schema missing 'ops' property")
	}
	if len(schema.Required) != 1 || schema.Required[0] != "ops" {
		t.Errorf("Required = %v, want [ops]", schema.Required)
	}
}

func TestApplyOpsSetStatusAndOutput(t *testing.T) {
	p := ledgerPlan()
	results := applyOps(p, []planOp{
		{Op: "set_status", StepID: "s1", Status: "done"},
		{Op: "set_output", StepID: "s1", Output: "spec.md"},
	})
	if len(results) != 2 || !results[0].OK || !results[1].OK {
		t.Fatalf("results = %+v, want both ok", results)
	}
	if p.Steps[0].Status != gantry.StepDone || p.Steps[0].Output != "spec.md" {
		t.Errorf("s1 not mutated: %+v", p.Steps[0])
	}
	if p.Steps[1].Status != gantry.StepPending {
		t.Errorf("s2 must be untouched: %+v", p.Steps[1])
	}
}

func TestApplyOpsAddStepMintsFreshID(t *testing.T) {
	p := ledgerPlan()
	results := applyOps(p, []planOp{{
		Op:                 "add_step",
		Description:        "write docs",
		AcceptanceCriteria: []string{"README updated", "godoc clean"},
	}})
	if !results[0].OK || results[0].StepID != "s3" {
		t.Fatalf("result = %+v, want ok with fresh id s3", results[0])
	}
	if len(p.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(p.Steps))
	}
	got := p.Steps[2]
	if got.ID != "s3" || got.Description != "write docs" || got.Status != gantry.StepPending {
		t.Errorf("appended step = %+v, want (s3, write docs, pending)", got)
	}
	if got.AcceptanceCriteria != "README updated; godoc clean" {
		t.Errorf("criteria = %q, want criteria joined with '; '", got.AcceptanceCriteria)
	}
}

func TestApplyOpsPerOpErrors(t *testing.T) {
	p := ledgerPlan()
	results := applyOps(p, []planOp{
		{Op: "set_status", StepID: "ghost", Status: "done"},  // unknown step
		{Op: "set_status", StepID: "s1", Status: "sideways"}, // invalid status
		{Op: "add_step"}, // missing description
		{Op: "warp"},     // unknown op
		{Op: "set_status", StepID: "s2", Status: "active"}, // still applies
	})
	for i := 0; i < 4; i++ {
		if results[i].OK || results[i].Error == "" {
			t.Errorf("results[%d] = %+v, want a per-op error", i, results[i])
		}
	}
	if !strings.Contains(results[0].Error, "ghost") {
		t.Errorf("unknown-step error must name the id: %q", results[0].Error)
	}
	if !results[4].OK {
		t.Errorf("op after failures must still apply: %+v", results[4])
	}
	if p.Steps[1].Status != gantry.StepActive {
		t.Errorf("later op not applied after earlier failures: %+v", p.Steps[1])
	}
	if len(p.Steps) != 2 {
		t.Errorf("failed add_step must not append; steps = %d", len(p.Steps))
	}
	if p.Steps[0].Status != gantry.StepActive {
		t.Errorf("failed ops must not mutate s1: %+v", p.Steps[0])
	}
}

func TestMintStepIDSkipsCollisions(t *testing.T) {
	fresh := &gantry.Plan{Steps: []gantry.PlanStep{{ID: "s1"}}}
	if got := mintStepID(fresh); got != "s2" {
		t.Errorf("mintStepID = %q, want s2", got)
	}
	collide := &gantry.Plan{Steps: []gantry.PlanStep{{ID: "custom"}, {ID: "s3"}}}
	if got := mintStepID(collide); got != "s4" {
		t.Errorf("mintStepID = %q, want s4 (s3 is taken)", got)
	}
}

func TestRunUpdatePlanEnvelopeErrors(t *testing.T) {
	if content, isErr := runUpdatePlan(nil, json.RawMessage(`{"ops":[{"op":"add_step","description":"x"}]}`)); !isErr || !strings.Contains(content, "no plan") {
		t.Errorf("nil plan: (%q, %v), want an error result", content, isErr)
	}
	p := ledgerPlan()
	if content, isErr := runUpdatePlan(p, json.RawMessage(`not json`)); !isErr || !strings.Contains(content, "invalid input") {
		t.Errorf("malformed input: (%q, %v), want an error result", content, isErr)
	}
	if content, isErr := runUpdatePlan(p, json.RawMessage(`{"ops":[]}`)); !isErr || !strings.Contains(content, "non-empty") {
		t.Errorf("empty ops: (%q, %v), want an error result", content, isErr)
	}
}

func TestRunUpdatePlanReportsPerOpResults(t *testing.T) {
	p := ledgerPlan()
	content, isErr := runUpdatePlan(p, json.RawMessage(
		`{"ops":[{"op":"set_status","step_id":"s1","status":"done"},{"op":"set_status","step_id":"ghost","status":"done"}]}`))
	if isErr {
		t.Fatalf("per-op failures must not be an envelope error: %q", content)
	}
	var out struct {
		Results []opResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		t.Fatalf("result content is not valid JSON: %v", err)
	}
	if len(out.Results) != 2 || !out.Results[0].OK || out.Results[1].OK {
		t.Errorf("Results = %+v, want [ok, error]", out.Results)
	}
	if p.Steps[0].Status != gantry.StepDone {
		t.Errorf("s1 not mutated: %+v", p.Steps[0])
	}
}

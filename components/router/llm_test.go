package router_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/router"
	"github.com/farazhassan/gantry/eval"
)

// newRoutes builds the standard three-route registry used by LLMRouter tests.
func newRoutes(t *testing.T) *router.Registry {
	t.Helper()
	reg := router.NewRegistry()
	add := func(key, desc string) {
		t.Helper()
		if err := reg.Add(key, desc, newTestAgent(t)); err != nil {
			t.Fatalf("Add(%q): %v", key, err)
		}
	}
	add("billing", "Invoices, payments, refunds.")
	add("support", "Product help and troubleshooting.")
	add("general", "Anything else.")
	return reg
}

// routeCall scripts one LLM reply that calls the route tool with key.
func routeCall(key string) gantry.LLMResponse {
	return gantry.LLMResponse{
		ToolCalls: []gantry.ToolCall{{
			ID:    "call-1",
			Name:  "route",
			Input: json.RawMessage(`{"route":"` + key + `","reason":"scripted"}`),
		}},
		StopReason: gantry.StopReasonToolUse,
	}
}

func TestLLMRouterHappyPath(t *testing.T) {
	reg := newRoutes(t)
	mock := eval.NewMockLLMClient(routeCall("billing"))
	lr, err := router.NewLLMRouter(mock, reg, "general")
	if err != nil {
		t.Fatalf("NewLLMRouter: %v", err)
	}

	key, err := lr.Classify(context.Background(), "why was I charged twice?", nil)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if key != "billing" {
		t.Errorf("key = %q, want billing", key)
	}

	reqs := mock.Requests()
	if len(reqs) != 1 {
		t.Fatalf("LLM requests = %d, want 1", len(reqs))
	}
	req := reqs[0]

	// Exactly one tool: the forced route tool.
	if len(req.Tools) != 1 || req.Tools[0].Name != "route" {
		t.Fatalf("Tools = %+v, want exactly the route tool", req.Tools)
	}
	var schema struct {
		Properties struct {
			Route struct {
				Enum []string `json:"enum"`
			} `json:"route"`
			Reason struct {
				Type string `json:"type"`
			} `json:"reason"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(req.Tools[0].Schema, &schema); err != nil {
		t.Fatalf("route schema is not valid JSON: %v", err)
	}
	if want := []string{"billing", "support", "general"}; !reflect.DeepEqual(schema.Properties.Route.Enum, want) {
		t.Errorf("route enum = %v, want %v (registry order)", schema.Properties.Route.Enum, want)
	}
	if schema.Properties.Reason.Type != "string" {
		t.Errorf("reason type = %q, want string", schema.Properties.Reason.Type)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "route" {
		t.Errorf("required = %v, want [route]", schema.Required)
	}

	// The tool call is forced.
	if req.ToolChoice == nil || req.ToolChoice.Mode != gantry.ToolChoiceTool || req.ToolChoice.Name != "route" {
		t.Errorf("ToolChoice = %+v, want {Mode: tool, Name: route}", req.ToolChoice)
	}

	// The system prompt lists every route with its description.
	for _, want := range []string{
		"billing: Invoices, payments, refunds.",
		"support: Product help and troubleshooting.",
		"general: Anything else.",
	} {
		if !strings.Contains(req.System, want) {
			t.Errorf("system prompt missing %q\nsystem: %s", want, req.System)
		}
	}

	// The final message is the routed input as a user message.
	last := req.Messages[len(req.Messages)-1]
	if last.Role != gantry.RoleUser || last.Content != "why was I charged twice?" {
		t.Errorf("last message = (%s, %q), want the routed input as a user message", last.Role, last.Content)
	}
}

func TestLLMRouterUnknownKeyFallsBack(t *testing.T) {
	mock := eval.NewMockLLMClient(routeCall("shipping")) // not registered
	lr, err := router.NewLLMRouter(mock, newRoutes(t), "general")
	if err != nil {
		t.Fatalf("NewLLMRouter: %v", err)
	}
	key, err := lr.Classify(context.Background(), "where is my parcel?", nil)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if key != "general" {
		t.Errorf("key = %q, want general (fallback)", key)
	}
}

func TestLLMRouterMalformedOutputFallsBack(t *testing.T) {
	cases := map[string]gantry.LLMResponse{
		"invalid tool-call JSON": {ToolCalls: []gantry.ToolCall{{ID: "c1", Name: "route", Input: json.RawMessage(`not json`)}}},
		"no tool call at all":    {Content: "billing"},
		"wrong tool name":        {ToolCalls: []gantry.ToolCall{{ID: "c1", Name: "other", Input: json.RawMessage(`{"route":"billing"}`)}}},
		"empty route key":        {ToolCalls: []gantry.ToolCall{{ID: "c1", Name: "route", Input: json.RawMessage(`{"reason":"no route field"}`)}}},
	}
	for name, resp := range cases {
		t.Run(name, func(t *testing.T) {
			lr, err := router.NewLLMRouter(eval.NewMockLLMClient(resp), newRoutes(t), "general")
			if err != nil {
				t.Fatalf("NewLLMRouter: %v", err)
			}
			key, err := lr.Classify(context.Background(), "hello", nil)
			if err != nil {
				t.Fatalf("Classify: %v", err)
			}
			if key != "general" {
				t.Errorf("key = %q, want general (fallback)", key)
			}
		})
	}
}

func TestLLMRouterNoFallbackIsErrNoRoute(t *testing.T) {
	lr, err := router.NewLLMRouter(eval.NewMockLLMClient(routeCall("shipping")), newRoutes(t), "")
	if err != nil {
		t.Fatalf("NewLLMRouter: %v", err)
	}
	_, err = lr.Classify(context.Background(), "hello", nil)
	if !errors.Is(err, router.ErrNoRoute) {
		t.Errorf("err = %v, want ErrNoRoute", err)
	}
}

func TestLLMRouterTransportErrorPropagates(t *testing.T) {
	boom := errors.New("boom")
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{{Err: boom}})
	lr, err := router.NewLLMRouter(mock, newRoutes(t), "general")
	if err != nil {
		t.Fatalf("NewLLMRouter: %v", err)
	}
	_, err = lr.Classify(context.Background(), "hello", nil)
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom (transport errors do NOT fall back)", err)
	}
	if errors.Is(err, router.ErrNoRoute) {
		t.Errorf("transport error must not read as ErrNoRoute")
	}
}

func TestLLMRouterRecentTranscriptFilteredAndCapped(t *testing.T) {
	mock := eval.NewMockLLMClient(routeCall("support"))
	lr, err := router.NewLLMRouter(mock, newRoutes(t), "")
	if err != nil {
		t.Fatalf("NewLLMRouter: %v", err)
	}
	recent := []gantry.Message{
		{Role: gantry.RoleUser, Content: "u1"},
		{Role: gantry.RoleAssistant, Content: "a1", ToolCalls: []gantry.ToolCall{{ID: "t1", Name: "x", Input: json.RawMessage(`{}`)}}}, // dropped: tool call
		{Role: gantry.RoleTool, Content: "result", ToolCallID: "t1"},                                                                   // dropped: tool result
		{Role: gantry.RoleUser, Content: "u2"},
		{Role: gantry.RoleAssistant, Content: "a2"},
		{Role: gantry.RoleUser, Content: "u3"},
		{Role: gantry.RoleAssistant, Content: "a3"},
		{Role: gantry.RoleUser, Content: "u4"},
		{Role: gantry.RoleAssistant, Content: "a4"},
	}
	if _, err := lr.Classify(context.Background(), "new question", recent); err != nil {
		t.Fatalf("Classify: %v", err)
	}
	// Plain user/assistant messages are u1,u2,a2,u3,a3,u4,a4 (7); capped to the
	// last 6, then the routed input is appended.
	got := mock.Requests()[0].Messages
	want := []string{"u2", "a2", "u3", "a3", "u4", "a4", "new question"}
	if len(got) != len(want) {
		t.Fatalf("messages = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Content != w {
			t.Errorf("message[%d] = %q, want %q", i, got[i].Content, w)
		}
	}
}

func TestNewLLMRouterValidation(t *testing.T) {
	reg := newRoutes(t)
	if _, err := router.NewLLMRouter(nil, reg, ""); err == nil {
		t.Errorf("nil client accepted, want an error")
	}
	if _, err := router.NewLLMRouter(eval.NewMockLLMClient(), nil, ""); err == nil {
		t.Errorf("nil registry accepted, want an error")
	}
	if _, err := router.NewLLMRouter(eval.NewMockLLMClient(), router.NewRegistry(), ""); err == nil {
		t.Errorf("empty registry accepted, want an error")
	}
	if _, err := router.NewLLMRouter(eval.NewMockLLMClient(), reg, "nope"); err == nil {
		t.Errorf("unregistered fallback accepted, want an error")
	}
}

func TestChainRulesFirstThenLLM(t *testing.T) {
	reg := newRoutes(t)
	mock := eval.NewMockLLMClient(routeCall("support"))
	lr, err := router.NewLLMRouter(mock, reg, "general")
	if err != nil {
		t.Fatalf("NewLLMRouter: %v", err)
	}
	c := router.Chain(router.NewRuleRouter(prefixRule("bill:", "billing")), lr)

	// A rule hit never consults the LLM.
	key, err := c.Classify(context.Background(), "bill: refund order 7", nil)
	if err != nil || key != "billing" {
		t.Fatalf("rule hit = (%q, %v), want (billing, nil)", key, err)
	}
	if n := len(mock.Requests()); n != 0 {
		t.Fatalf("LLM consulted %d times on a rule hit, want 0", n)
	}

	// A rule miss falls through to the LLM.
	key, err = c.Classify(context.Background(), "how do I reset my password?", nil)
	if err != nil || key != "support" {
		t.Fatalf("rule miss = (%q, %v), want (support, nil)", key, err)
	}
	if n := len(mock.Requests()); n != 1 {
		t.Errorf("LLM consulted %d times after a rule miss, want 1", n)
	}
}

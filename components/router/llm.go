package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/farazhassan/gantry"
)

// routeToolName is the single forced tool the classifier model must call.
const routeToolName = "route"

// maxRecent caps how many trailing plain-text transcript messages are included
// as classification context, keeping the classifier call cheap.
const maxRecent = 6

// LLMRouter classifies with a single forced tool call against a designated —
// typically cheap — LLMClient. It is NOT an agent loop: one Generate call, one
// guaranteed tool call (ToolChoice{Mode: ToolChoiceTool, Name: "route"}), no
// tool execution, no iteration.
//
// No generation span is opened around the call: the span helpers
// (startGeneration, tracerFrom) are unexported in package gantry, and Classify
// runs outside any agent run loop, so no Tracer is reachable from its ctx.
// Revisit if a future plan exports those helpers.
type LLMRouter struct {
	client   gantry.LLMClient
	registry *Registry
	fallback string
}

// compile-time check: LLMRouter implements Classifier.
var _ Classifier = (*LLMRouter)(nil)

// NewLLMRouter builds an LLM-backed classifier over reg. fallback is the route
// key returned when the model's output is malformed (no route tool call,
// invalid JSON arguments, or an unregistered key); it must be a registered key.
// An empty fallback means malformed output yields an error wrapping ErrNoRoute
// instead. Transport errors from client always propagate — the fallback covers
// malformed output only.
func NewLLMRouter(client gantry.LLMClient, reg *Registry, fallback string) (*LLMRouter, error) {
	if client == nil {
		return nil, errors.New("router: NewLLMRouter requires a non-nil client")
	}
	if reg == nil || len(reg.Keys()) == 0 {
		return nil, errors.New("router: NewLLMRouter requires a registry with at least one route")
	}
	if fallback != "" {
		if _, ok := reg.Get(fallback); !ok {
			return nil, fmt.Errorf("router: fallback %q is not a registered route", fallback)
		}
	}
	return &LLMRouter{client: client, registry: reg, fallback: fallback}, nil
}

// Classify sends one forced-tool-call request: a system prompt listing every
// route, up to maxRecent plain-text messages of recent as context, and input
// as the final user message. The forced call's "route" argument is validated
// against the registry; anything malformed resolves to the fallback (or an
// ErrNoRoute error when no fallback is configured).
func (r *LLMRouter) Classify(ctx context.Context, input string, recent []gantry.Message) (string, error) {
	keys := r.registry.Keys()
	def, err := routeToolDef(keys)
	if err != nil {
		return "", err
	}
	msgs := append(recentTranscript(recent, maxRecent), gantry.Message{Role: gantry.RoleUser, Content: input})
	req := gantry.LLMRequest{
		System:     r.systemPrompt(keys),
		Messages:   msgs,
		Tools:      []gantry.ToolDef{def},
		ToolChoice: &gantry.ToolChoice{Mode: gantry.ToolChoiceTool, Name: routeToolName},
	}
	resp, err := r.client.Generate(ctx, req)
	if err != nil {
		return "", err
	}
	if key, ok := r.parseRoute(resp); ok {
		return key, nil
	}
	if r.fallback != "" {
		return r.fallback, nil
	}
	return "", fmt.Errorf("router: model returned no usable route: %w", ErrNoRoute)
}

// systemPrompt lists every route key with its description, in registry order.
func (r *LLMRouter) systemPrompt(keys []string) string {
	var b strings.Builder
	b.WriteString("You are a request router. Read the user's message and call the ")
	b.WriteString(routeToolName)
	b.WriteString(" tool with the single best route key for it.\n\nRoutes:\n")
	for _, k := range keys {
		desc, _ := r.registry.Description(k)
		b.WriteString("- ")
		b.WriteString(k)
		if desc != "" {
			b.WriteString(": ")
			b.WriteString(desc)
		}
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// routeToolDef builds the forced tool whose schema constrains "route" to the
// registered keys: {"route": {"enum": [keys...]}, "reason": string}.
func routeToolDef(keys []string) (gantry.ToolDef, error) {
	schema, err := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"route": map[string]any{
				"type":        "string",
				"enum":        keys,
				"description": "The route key of the agent that should handle the request.",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "One short sentence explaining the choice.",
			},
		},
		"required": []string{"route"},
	})
	if err != nil {
		return gantry.ToolDef{}, fmt.Errorf("router: marshal route schema: %w", err)
	}
	return gantry.ToolDef{
		Name:        routeToolName,
		Description: "Select the agent that should handle the user's request.",
		Schema:      schema,
	}, nil
}

// parseRoute extracts a registered route key from the forced tool call. It
// reports false on any malformed shape: no route tool call, invalid JSON
// arguments, an empty key, or a key that is not registered.
func (r *LLMRouter) parseRoute(resp gantry.LLMResponse) (string, bool) {
	for _, tc := range resp.ToolCalls {
		if tc.Name != routeToolName {
			continue
		}
		var args struct {
			Route string `json:"route"`
		}
		if err := json.Unmarshal(tc.Input, &args); err != nil {
			return "", false
		}
		if _, ok := r.registry.Get(args.Route); !ok {
			return "", false
		}
		return args.Route, true
	}
	return "", false
}

// recentTranscript returns the last max plain-text user/assistant messages.
// Tool-call and tool-result messages are dropped: providers require tool
// results to follow their paired tool-use blocks, and a truncated tail cannot
// guarantee that pairing.
func recentTranscript(msgs []gantry.Message, max int) []gantry.Message {
	var out []gantry.Message
	for _, m := range msgs {
		if m.Role != gantry.RoleUser && m.Role != gantry.RoleAssistant {
			continue
		}
		if len(m.ToolCalls) > 0 || m.ToolCallID != "" {
			continue
		}
		out = append(out, m)
	}
	if len(out) > max {
		out = out[len(out)-max:]
	}
	return out
}

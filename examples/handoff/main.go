package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/session"
)

// handoffToolDef is the definition-only tool the router calls to route a
// conversation. Like tool.Client's client tools it has no server-side Invoke;
// unlike them, the run does not suspend for a caller to fulfill it — the
// routing middleware converts the call into a DoneHandoff termination instead.
var handoffToolDef = gantry.ToolDef{
	Name:        "handoff",
	Description: "Hand this conversation to a specialist agent. Call it when another agent is better suited to answer.",
	Schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "target": {"type": "string", "description": "Registry key of the specialist agent."},
    "reason": {"type": "string", "description": "Why this conversation is being handed off."}
  },
  "required": ["target"]
}`),
}

// router is a Component installing the routing middleware pair:
//
//   - PhaseStart: advertise the handoff ToolDef (mirrors tool.Client's
//     advertise middleware).
//   - PhasePostLLM (after DefaultPostLLMHandler): if the model called
//     "handoff", fulfill the call with a tool-role message so the transcript
//     stays well-formed for the target agent, clear the pending calls, and
//     terminate with state.Handoff + Done/DoneHandoff.
//
// Placement note, verified against components/tool/client.go: client tools
// suspend at PhaseObserve so same-iteration server-side tool results still
// land in the transcript before Done is set. This router terminates at
// PhasePostLLM because handoff is the ONLY tool it advertises — there are no
// server results to preserve, and ending before PhaseToolExec avoids
// dispatching a tool that has no Invoke. A router mixing handoff with
// server-side tools should move detection to PhaseObserve, like client.go.
type router struct{}

func (router) Install(a *gantry.Agent) error {
	if err := a.UseNamed(gantry.PhaseStart, "examples/handoff:advertise", func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			s.Tools = append(s.Tools, handoffToolDef)
			return next(ctx, s)
		}
	}); err != nil {
		return err
	}
	return a.UseNamed(gantry.PhasePostLLM, "examples/handoff:route", func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if err := next(ctx, s); err != nil {
				return err
			}
			for _, call := range s.PendingToolCalls {
				if call.Name != "handoff" {
					continue
				}
				var in struct {
					Target string `json:"target"`
					Reason string `json:"reason"`
				}
				if err := json.Unmarshal(call.Input, &in); err != nil || in.Target == "" {
					// Malformed handoff call: leave the run alone; the loop
					// dispatches it as an ordinary (unknown) tool call and the
					// model sees the resulting error.
					continue
				}
				// Fulfill the call in the transcript: an assistant tool call
				// must be answered before the next LLM call, and the target
				// agent resumes from exactly these messages.
				s.Messages = append(s.Messages, gantry.Message{
					Role:       gantry.RoleTool,
					ToolCallID: call.ID,
					Content:    "transferring to " + in.Target,
				})
				s.PendingToolCalls = nil
				s.Handoff = &gantry.Handoff{Target: in.Target, Mode: gantry.HandoffTransfer, Reason: in.Reason}
				s.Done = true
				s.DoneReason = gantry.DoneHandoff
				return nil
			}
			return nil
		}
	})
}

// RunExample wires a router agent and a billing agent behind one session and
// runs a single turn that hands off. It returns the terminal State so the
// test can assert on the routed outcome.
func RunExample(ctx context.Context) (*gantry.State, error) {
	// The router's scripted model answers with a handoff tool call.
	routerLLM := eval.NewMockLLMClient(gantry.LLMResponse{
		Content: "This is a billing question.",
		ToolCalls: []gantry.ToolCall{{
			ID:    "call-1",
			Name:  "handoff",
			Input: json.RawMessage(`{"target":"billing","reason":"invoice question"}`),
		}},
		StopReason: gantry.StopReasonToolUse,
	})
	routerAgent, err := gantry.NewAgent(
		gantry.WithLLM(routerLLM),
		gantry.WithComponents(router{}),
	)
	if err != nil {
		return nil, err
	}

	// The billing specialist answers plainly.
	billingLLM := eval.NewMockLLMClient(gantry.LLMResponse{
		Content:    "I checked your invoice: the duplicate charge has been refunded.",
		StopReason: gantry.StopReasonEnd,
	})
	billingAgent, err := gantry.NewAgent(gantry.WithLLM(billingLLM))
	if err != nil {
		return nil, err
	}

	agents := map[string]*gantry.Agent{"billing": billingAgent}
	mgr := session.NewManager(routerAgent, checkpointer.NewInMemory(),
		session.WithResolver(func(_ string, h *gantry.Handoff) *gantry.Agent {
			return agents[h.Target] // nil for unknown targets -> ErrHandoffTargetUnknown
		}))

	return mgr.Session("user-1").Run(ctx, "my invoice has a duplicate charge")
}

func main() {
	state, err := RunExample(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("final output:", state.FinalOutput)
	fmt.Println("done reason: ", state.DoneReason)
}

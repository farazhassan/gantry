package gantry

import "context"

// MaxIterationsWrapUpPrompt is the user message appended to the transcript
// when a run exhausts its iteration cap without finishing. The agent then gets
// one tool-less LLM turn (ToolChoiceNone) to answer from what it gathered, so a
// capped run ends with a real FinalOutput instead of an empty one. It is
// exported so layers that continue a capped transcript (task.Driver) can find
// and drop the exchange.
const MaxIterationsWrapUpPrompt = "You have reached the iteration limit and cannot call any more tools. " +
	"Give your best final answer now, using only what you have already gathered, " +
	"and state clearly anything you were unable to determine."

// wrapUp runs once, after the main loop exits on the iteration cap with
// state.Done still false. It appends MaxIterationsWrapUpPrompt and re-runs the
// agent's phases (skipping PhaseStart/PhaseEnd) up to and including
// PhasePostLLM with ToolChoiceNone on the context, so middleware, tracing,
// streaming, and usage accounting apply exactly as on any other turn.
//
// Termination: a reason a middleware set during the pass (e.g. the limiter's
// DoneBudgetExceeded, a guardrail block) stands; otherwise — no reason, or the
// DoneNoToolCalls DefaultPostLLMHandler sets on a plain answer — the run is
// reported as DoneMaxIterations. A phase error is returned unchanged.
func (a *Agent) wrapUp(ctx context.Context, tracer Tracer, state *State) error {
	state.Messages = append(state.Messages, Message{Role: RoleUser, Content: MaxIterationsWrapUpPrompt})
	ctx = withToolChoice(ctx, &ToolChoice{Mode: ToolChoiceNone})

	for _, ph := range a.phases {
		if ph == PhaseStart || ph == PhaseEnd {
			continue
		}
		if state.Done {
			break
		}
		if err := a.runPhase(ctx, tracer, ph, state); err != nil {
			return err
		}
		if ph == PhasePostLLM {
			// Before emitPhaseEffects, so a stray call never surfaces as a
			// tool_call event.
			dropWrapUpToolCalls(state)
		}
		if err := a.emitPhaseEffects(ctx, ph, state); err != nil {
			return err
		}
		if ph == PhasePostLLM {
			break
		}
	}

	state.Done = true
	if state.DoneReason == "" || state.DoneReason == DoneNoToolCalls {
		state.DoneReason = DoneMaxIterations
	}
	return nil
}

// dropWrapUpToolCalls discards tool calls a model returned on the wrap-up turn
// despite ToolChoiceNone: they are never executed, and they are stripped from
// the assistant message so the transcript never ends with a tool call that has
// no result. The response text is kept as FinalOutput.
func dropWrapUpToolCalls(state *State) {
	if len(state.PendingToolCalls) == 0 {
		return
	}
	state.PendingToolCalls = nil
	if n := len(state.Messages); n > 0 && state.Messages[n-1].Role == RoleAssistant {
		state.Messages[n-1].ToolCalls = nil
	}
	if state.LastResponse != nil {
		state.FinalOutput = state.LastResponse.Content
	}
}

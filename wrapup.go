package gantry

import "context"

// MaxIterationsWrapUpPrompt is the user message shown to the model on the
// one tool-less wrap-up turn (ToolChoiceNone) a run gets when it exhausts its
// iteration cap without finishing, so a capped run ends with a real
// FinalOutput instead of an empty one. It is shown for that pass only and is
// removed from the stored transcript afterwards, so later turns built on the
// transcript never see it. It is exported for documentation and tests.
const MaxIterationsWrapUpPrompt = "You have reached the iteration limit and cannot call any more tools. " +
	"Give your best final answer now, using only what you have already gathered, " +
	"and state clearly anything you were unable to determine."

// wrapUp runs once, after the main loop exits on the iteration cap with
// state.Done still false. It appends MaxIterationsWrapUpPrompt and re-runs the
// agent's phases (skipping PhaseStart/PhaseEnd) up to and including
// PhasePostLLM with ToolChoiceNone on the context, so middleware, tracing,
// streaming, and usage accounting apply exactly as on any other turn. The
// prompt is removed from state.Messages on every exit path, so the stored
// transcript reads "... tool results -> wrap-up answer". Wrap-up phase events
// carry Iteration == maxIterations.
//
// Termination: a reason a middleware set during the pass (e.g. the limiter's
// DoneBudgetExceeded, a guardrail block) stands; otherwise — no reason, or the
// DoneNoToolCalls DefaultPostLLMHandler sets on a plain answer — the run is
// reported as DoneMaxIterations. If components/critic rejects the wrap-up
// answer its verdict stands: the run ends DoneMaxIterations with an empty
// FinalOutput. A phase error is returned unchanged.
func (a *Agent) wrapUp(ctx context.Context, tracer Tracer, state *State) error {
	// A state checkpointed mid-wrap-up still carries the prompt; drop it so a
	// resumed pass doesn't send it twice.
	removeWrapUpPrompt(state)
	state.Messages = append(state.Messages, Message{Role: RoleUser, Content: MaxIterationsWrapUpPrompt})
	defer removeWrapUpPrompt(state)
	ctx = withToolChoice(ctx, &ToolChoice{Mode: ToolChoiceNone})

	for _, ph := range a.phases {
		if ph == PhaseStart || ph == PhaseEnd {
			continue
		}
		if state.Done {
			break
		}
		if err := ctx.Err(); err != nil {
			return err
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
		if state.Messages[n-1].Content == "" {
			// Nothing left: an empty assistant message would be sent as
			// null content on the next turn.
			state.Messages = state.Messages[:n-1]
		} else {
			state.Messages[n-1].ToolCalls = nil
		}
	}
	if state.LastResponse != nil {
		state.FinalOutput = state.LastResponse.Content
	}
}

// removeWrapUpPrompt deletes the last user message whose content is exactly
// MaxIterationsWrapUpPrompt. It searches by content, not by index, because
// assemble_context middleware (e.g. a compactor) may rewrite earlier messages
// during the pass.
func removeWrapUpPrompt(state *State) {
	for i := len(state.Messages) - 1; i >= 0; i-- {
		if m := state.Messages[i]; m.Role == RoleUser && m.Content == MaxIterationsWrapUpPrompt {
			state.Messages = append(state.Messages[:i:i], state.Messages[i+1:]...)
			return
		}
	}
}

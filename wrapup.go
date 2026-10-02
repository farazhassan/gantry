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
// state.Done still false. It re-runs the agent's phases (skipping
// PhaseStart/PhaseEnd) up to and including PhasePostLLM with ToolChoiceNone on
// the context, so middleware, tracing, streaming, and usage accounting apply
// exactly as on any other turn. MaxIterationsWrapUpPrompt is appended just
// before PhaseLLMCall runs, so context assembly (e.g. compaction) sees the same
// transcript as a normal turn, and it is removed from state.Messages on every
// exit path, so the stored transcript reads "... tool results -> wrap-up
// answer". Tool calls the model returns anyway are stripped before
// PhasePostLLM, so DefaultPostLLMHandler and PostLLM middleware see a plain
// text answer. Wrap-up phase events carry Iteration == maxIterations.
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
	defer removeWrapUpPrompt(state)
	ctx = withToolChoice(ctx, &ToolChoice{Mode: ToolChoiceNone})

	prompted, strippedEmpty := false, false
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
		if ph == PhaseLLMCall && !prompted {
			prompted = true
			state.Messages = append(state.Messages, Message{Role: RoleUser, Content: MaxIterationsWrapUpPrompt})
		}
		if err := a.runPhase(ctx, tracer, ph, state); err != nil {
			return err
		}
		if ph == PhaseLLMCall {
			strippedEmpty = stripWrapUpToolCalls(state)
		}
		if ph == PhasePostLLM && strippedEmpty {
			dropTrailingEmptyAssistant(state)
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

// stripWrapUpToolCalls discards tool calls a model returned on the wrap-up
// turn despite ToolChoiceNone, before PhasePostLLM: they are never executed,
// and DefaultPostLLMHandler and PostLLM middleware see a plain text answer
// (Done, FinalOutput = the response text), so the transcript never ends with
// a tool call that has no result. It reports whether calls were stripped from
// a response with no text, whose assistant message must then be dropped.
func stripWrapUpToolCalls(state *State) bool {
	resp := state.LastResponse
	if resp == nil || len(resp.ToolCalls) == 0 {
		return false
	}
	stripped := *resp
	stripped.ToolCalls = nil
	state.LastResponse = &stripped
	return stripped.Content == ""
}

// dropTrailingEmptyAssistant removes the empty assistant message
// DefaultPostLLMHandler appends for a stripped, text-less wrap-up response:
// it would be sent as null content on the next turn.
func dropTrailingEmptyAssistant(state *State) {
	if n := len(state.Messages); n > 0 {
		if m := state.Messages[n-1]; m.Role == RoleAssistant && m.Content == "" && len(m.ToolCalls) == 0 {
			state.Messages = state.Messages[:n-1]
		}
	}
}

// removeWrapUpPrompt deletes the last user message whose content is exactly
// MaxIterationsWrapUpPrompt. It searches by content, not by index, because
// PhaseLLMCall/PhasePostLLM middleware may append or rewrite messages after
// the prompt during the pass.
func removeWrapUpPrompt(state *State) {
	for i := len(state.Messages) - 1; i >= 0; i-- {
		if m := state.Messages[i]; m.Role == RoleUser && m.Content == MaxIterationsWrapUpPrompt {
			state.Messages = append(state.Messages[:i:i], state.Messages[i+1:]...)
			return
		}
	}
}

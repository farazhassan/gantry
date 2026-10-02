package gantry

import "context"

// MaxIterationsWrapUpPrompt is the user message shown to the model on the
// one tool-less wrap-up turn (ToolChoiceNone) a run gets when it exhausts its
// iteration cap without finishing, giving the model a chance to answer from
// what it gathered (FinalOutput can still be empty). It is shown for that pass only and is
// removed from the stored transcript afterwards, so later turns built on the
// transcript never see it. It is exported for documentation and tests.
const MaxIterationsWrapUpPrompt = "You have reached the iteration limit and cannot call any more tools. " +
	"Give your best final answer now, using only what you have already gathered, " +
	"and state clearly anything you were unable to determine."

// wrapUpNoAnswer is the assistant message content recorded for a wrap-up turn
// that produced no text (or only tool calls, which are stripped). Every
// PostLLM step — including components/transcript's persist — sees a valid,
// non-empty assistant message (provider APIs reject an assistant message with
// no content), while FinalOutput stays empty so the run still reads as
// unanswered.
const wrapUpNoAnswer = "No answer: reached the iteration limit without producing a final response."

// wrapUp runs once, after the main loop exits on the iteration cap with
// state.Done still false. It re-runs the agent's phases (skipping
// PhaseStart/PhaseEnd) up to and including PhasePostLLM with ToolChoiceNone on
// the context, so middleware, tracing, streaming, and usage accounting apply
// exactly as on any other turn. MaxIterationsWrapUpPrompt is appended just
// before PhaseLLMCall runs, so context assembly (e.g. compaction) sees the same
// transcript as a normal turn, and removed right after it (and on every other
// exit path), so PostLLM middleware and the stored transcript read "... tool
// results -> wrap-up answer". Tool calls the model returns anyway are stripped
// before PhasePostLLM, so DefaultPostLLMHandler and PostLLM middleware see a
// plain text answer, already marked DoneMaxIterations; a text-less answer is
// recorded as the wrapUpNoAnswer placeholder message with an empty FinalOutput.
// Wrap-up phase events carry Iteration == maxIterations.
//
// Termination: a reason a middleware set during the pass (e.g. the limiter's
// DoneBudgetExceeded, a guardrail block) stands; otherwise the run is reported
// as DoneMaxIterations — DefaultPostLLMHandler records it directly on the
// wrap-up turn, and wrapUp maps a remaining "" or DoneNoToolCalls (e.g. from a
// custom inner PostLLM handler) to it after the pass. If components/critic
// rejects the wrap-up answer its verdict stands: the run ends
// DoneMaxIterations with an empty FinalOutput. A phase error is returned
// unchanged.
func (a *Agent) wrapUp(ctx context.Context, tracer Tracer, state *State) error {
	// A state checkpointed mid-wrap-up still carries the prompt; drop it so a
	// resumed pass doesn't send it twice.
	removeWrapUpPrompt(state)
	defer removeWrapUpPrompt(state)
	ctx = withWrapUp(withToolChoice(ctx, &ToolChoice{Mode: ToolChoiceNone}))

	prompted := false
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
			// The model has consumed the prompt; drop it now so PostLLM
			// middleware (e.g. a checkpointer) never sees or saves it.
			removeWrapUpPrompt(state)
			stripWrapUpToolCalls(state)
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

// wrapUpKey marks the context of the max-iterations wrap-up pass, so
// DefaultPostLLMHandler can record DoneMaxIterations rather than
// DoneNoToolCalls for the wrap-up answer.
type wrapUpKey struct{}

func withWrapUp(ctx context.Context) context.Context {
	return context.WithValue(ctx, wrapUpKey{}, true)
}

func isWrapUp(ctx context.Context) bool {
	v, _ := ctx.Value(wrapUpKey{}).(bool)
	return v
}

// stripWrapUpToolCalls discards tool calls a model returned on the wrap-up
// turn despite ToolChoiceNone, before PhasePostLLM: they are never executed,
// and DefaultPostLLMHandler and PostLLM middleware see a plain text answer
// (Done, FinalOutput = the response text), so the transcript never ends with
// a tool call that has no result. LastResponse is replaced by a copy, never
// mutated in place.
func stripWrapUpToolCalls(state *State) {
	resp := state.LastResponse
	if resp == nil || len(resp.ToolCalls) == 0 {
		return
	}
	stripped := *resp
	stripped.ToolCalls = nil
	state.LastResponse = &stripped
}

// removeWrapUpPrompt deletes the last user message whose content is exactly
// MaxIterationsWrapUpPrompt. It searches by content, not by index, because
// PhaseLLMCall middleware may append or rewrite messages during the pass.
func removeWrapUpPrompt(state *State) {
	for i := len(state.Messages) - 1; i >= 0; i-- {
		if m := state.Messages[i]; m.Role == RoleUser && m.Content == MaxIterationsWrapUpPrompt {
			state.Messages = append(state.Messages[:i:i], state.Messages[i+1:]...)
			return
		}
	}
}

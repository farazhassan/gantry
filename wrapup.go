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
	removeTrailingWrapUpPrompt(state)
	// inj records where this pass injected the prompt (idx -1 while there is
	// nothing (left) to remove) and how many identical messages preceded it.
	inj := wrapUpInjection{idx: -1}
	defer func() { removeInjectedWrapUpPrompt(state, &inj) }()
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
			inj = wrapUpInjection{idx: len(state.Messages), prior: countWrapUpText(state.Messages)}
			state.Messages = append(state.Messages, Message{Role: RoleUser, Content: MaxIterationsWrapUpPrompt, wrapUp: true})
		}
		prev := state.LastResponse
		if err := a.runPhase(ctx, tracer, ph, state); err != nil {
			return err
		}
		if ph == PhaseLLMCall {
			// The model has consumed the prompt; drop it now so PostLLM
			// middleware (e.g. a checkpointer) never sees or saves it.
			removeInjectedWrapUpPrompt(state, &inj)
			// Only a response produced by this pass is stripped: if
			// middleware ended the pass without calling the LLM,
			// LastResponse is still the capped turn's, whose tool calls
			// the transcript holds.
			if state.LastResponse != prev {
				stripWrapUpToolCalls(state)
			}
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

// removeTrailingWrapUpPrompt drops a MaxIterationsWrapUpPrompt left by a state
// checkpointed mid-wrap-up (e.g. at PhaseLLMCall). JSON drops the wrapUp
// marker, so it is found by content: the last user message with the prompt's
// text that no assistant message follows. Middleware may have appended after
// it, so it need not be last. A real user message with identical content is
// never touched — reaching the cap takes at least one assistant turn after any
// user input, so an assistant message always follows it.
func removeTrailingWrapUpPrompt(state *State) {
	for i := len(state.Messages) - 1; i >= 0; i-- {
		m := state.Messages[i]
		if m.Role == RoleAssistant {
			return
		}
		if m.Role == RoleUser && m.Content == MaxIterationsWrapUpPrompt {
			state.Messages = append(state.Messages[:i:i], state.Messages[i+1:]...)
			state.ContextUsage = ContextUsage{} // not an append: anchor invalid
			return
		}
	}
}

// wrapUpInjection identifies the prompt a wrap-up pass injected: idx is where
// it was appended (-1 once removed or never injected) and prior is how many
// user messages with the prompt's text the transcript held just before.
type wrapUpInjection struct {
	idx, prior int
}

// removeInjectedWrapUpPrompt removes the prompt this pass injected. In order:
//  1. the last message carrying the wrapUp marker, wherever it now sits
//     (middleware such as the compactor may have reordered or shrunk the
//     transcript, or appended after it);
//  2. if middleware rebuilt messages field-by-field (dropping the marker), the
//     last RoleUser MaxIterationsWrapUpPrompt message at or after the
//     injection index;
//  3. if middleware also trimmed earlier messages so the prompt moved below
//     that index, the last such message — but only when there are more of
//     them than before injection, so a genuine message with identical content
//     is never removed in place of a prompt that was dropped.
//
// It then sets idx to -1, so a later call (the deferred cleanup) is a no-op.
func removeInjectedWrapUpPrompt(state *State, inj *wrapUpInjection) {
	start := inj.idx
	if start < 0 {
		return
	}
	inj.idx = -1
	remove := func(i int) {
		state.Messages = append(state.Messages[:i:i], state.Messages[i+1:]...)
		state.ContextUsage = ContextUsage{} // not an append: anchor invalid
	}
	for i := len(state.Messages) - 1; i >= 0; i-- {
		if state.Messages[i].wrapUp {
			remove(i)
			return
		}
	}
	last := -1
	for i := len(state.Messages) - 1; i >= 0; i-- {
		if isWrapUpText(state.Messages[i]) {
			last = i
			break
		}
	}
	if last >= 0 && (last >= start || countWrapUpText(state.Messages) > inj.prior) {
		remove(last)
	}
}

// isWrapUpText reports whether m has the wrap-up prompt's role and text.
func isWrapUpText(m Message) bool {
	return m.Role == RoleUser && m.Content == MaxIterationsWrapUpPrompt
}

// countWrapUpText counts messages with the wrap-up prompt's role and text.
func countWrapUpText(msgs []Message) int {
	n := 0
	for _, m := range msgs {
		if isWrapUpText(m) {
			n++
		}
	}
	return n
}

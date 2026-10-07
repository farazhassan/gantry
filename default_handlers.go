package gantry

import (
	"context"
	"errors"
	"strings"
)

// DefaultStartHandler seeds state.Messages with state.Input as a user message
// if state.Messages is empty and state.Input is non-empty. Memory middleware
// (Plan 2) overrides this by either replacing or prepending to Messages.
func DefaultStartHandler(ctx context.Context, state *State) error {
	if len(state.Messages) > 0 || state.Input == "" {
		return nil
	}
	state.Messages = append(state.Messages, Message{
		Role:    RoleUser,
		Content: state.Input,
	})
	return nil
}

// DefaultLLMCallHandler builds the inner handler for PhaseLLMCall using the
// supplied LLMClient. The Agent uses this internally; users can substitute it
// by passing a custom Handler via WithInnerHandler (Plan 2).
//
// When a RunStream sink is active AND the client implements StreamingLLMClient,
// the handler streams, emitting an event per populated StreamChunk field (see
// invokeLLM). In all other cases (plain Run, or a non-streaming client) it
// falls back to Generate — identical to the pre-streaming behavior.
func DefaultLLMCallHandler(client LLMClient) Handler {
	return func(ctx context.Context, state *State) error {
		req := LLMRequest{
			System:      state.System,
			Messages:    state.Messages,
			Tools:       state.Tools,
			ToolChoice:  ToolChoiceFrom(ctx),
			Temperature: temperatureFrom(ctx),
		}
		genCtx, gen := startGeneration(ctx, req)
		resp, err := invokeLLM(genCtx, client, state, req)
		if err == nil && resp.StopReason == StopReasonContextWindow && len(resp.ToolCalls) > 0 {
			// The window filled mid tool call, so its input may be cut off:
			// never run it. Report an overflow instead, so a
			// ContextOverflowHandler can shrink the prompt and retry. The
			// tokens were still spent.
			state.Usage = state.Usage.Add(resp.Usage)
			err = &ContextLengthError{Err: errors.New("context window filled during a tool call")}
		}
		gen.end(resp, err)
		if err != nil {
			return err
		}
		state.LastResponse = &resp
		state.Usage = state.Usage.Add(resp.Usage)
		if resp.Usage.InputTokens > 0 {
			// Only the prompt is anchored: OutputTokens can include hidden
			// reasoning that is never replayed, so the reply is estimated from
			// its visible content instead (see ContextUsage).
			state.ContextUsage = ContextUsage{
				PromptTokens: resp.Usage.InputTokens,
				MessageCount: len(req.Messages),
			}
		} else {
			state.ContextUsage = ContextUsage{}
		}
		return nil
	}
}

// invokeLLM streams when a sink is active and the client implements
// StreamingLLMClient, otherwise falls back to Generate. It returns the
// fully-aggregated response in both cases and does not mutate state, so the LLM
// call can be wrapped in a generation span by the caller. Each populated field
// on a chunk is emitted through emit as its own event, independently — a
// terminal metadata-only chunk (empty TextDelta/ReasoningDelta/RawFrame)
// emits nothing, but a chunk carrying more than one field emits more than one
// event.
func invokeLLM(ctx context.Context, client LLMClient, state *State, req LLMRequest) (LLMResponse, error) {
	if _, ok := SinkFrom(ctx); ok {
		if sc, ok := client.(StreamingLLMClient); ok {
			return sc.GenerateStream(ctx, req, func(ch StreamChunk) error {
				if ch.TextDelta != "" {
					if err := emit(ctx, Event{
						Type:      EventTextDelta,
						Iteration: state.Iteration,
						Phase:     PhaseLLMCall,
						TextDelta: ch.TextDelta,
					}); err != nil {
						return err
					}
				}
				if ch.ReasoningDelta != "" {
					if err := emit(ctx, Event{
						Type:           EventReasoningDelta,
						Iteration:      state.Iteration,
						Phase:          PhaseLLMCall,
						ReasoningDelta: ch.ReasoningDelta,
					}); err != nil {
						return err
					}
				}
				if len(ch.RawFrame) > 0 {
					if err := emit(ctx, Event{
						Type:      EventRaw,
						Iteration: state.Iteration,
						Phase:     PhaseLLMCall,
						RawFrame:  ch.RawFrame,
						RawSource: ch.RawSource,
					}); err != nil {
						return err
					}
				}
				return nil
			})
		}
	}
	return client.Generate(ctx, req)
}

// DefaultPostLLMHandler examines state.LastResponse. If the response has
// pending tool calls, they are copied into state.PendingToolCalls. If it has
// no tool calls, the loop is marked Done with DoneNoToolCalls and the LLM
// content becomes the FinalOutput.
//
// The assistant message itself is appended to state.Messages so the next
// LLM call (if any) sees the prior turn.
//
// On the max-iterations wrap-up turn the reason is DoneMaxIterations instead,
// so PostLLM middleware and checkpoints see the true terminal reason; and if
// the wrap-up answer has no text (empty or whitespace-only), the appended
// assistant message carries the wrapUpNoAnswer placeholder rather than empty
// content (which provider APIs reject on a later turn), while FinalOutput
// stays empty.
func DefaultPostLLMHandler(ctx context.Context, state *State) error {
	resp := state.LastResponse
	if resp == nil {
		// No LLM call happened (e.g. middleware short-circuited). Nothing to do.
		return nil
	}

	if len(resp.ToolCalls) == 0 && isWrapUp(ctx) {
		content, output := resp.Content, resp.Content
		if strings.TrimSpace(content) == "" {
			content, output = wrapUpNoAnswer, ""
		}
		state.Messages = append(state.Messages, Message{Role: RoleAssistant, Content: content})
		state.Done = true
		state.DoneReason = DoneMaxIterations
		state.FinalOutput = output
		return nil
	}

	// Append the assistant message to the transcript.
	state.Messages = append(state.Messages, Message{
		Role:      RoleAssistant,
		Content:   resp.Content,
		ToolCalls: resp.ToolCalls,
	})

	if len(resp.ToolCalls) == 0 {
		state.Done = true
		state.DoneReason = DoneNoToolCalls
		state.FinalOutput = resp.Content
		return nil
	}
	state.PendingToolCalls = append(state.PendingToolCalls[:0], resp.ToolCalls...)
	return nil
}

// DefaultObserveHandler folds completed ToolResults into the message
// transcript as RoleTool messages, then clears the pending/result slices
// so the next iteration starts fresh.
func DefaultObserveHandler(ctx context.Context, state *State) error {
	for _, r := range state.ToolResults {
		state.Messages = append(state.Messages, Message{
			Role:       RoleTool,
			Content:    r.Content,
			ToolCallID: r.CallID,
		})
	}
	state.ToolResults = state.ToolResults[:0]
	state.PendingToolCalls = state.PendingToolCalls[:0]
	return nil
}

package gantry

import "context"

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
// the handler streams, emitting an EventTextDelta per non-empty chunk. In all
// other cases (plain Run, or a non-streaming client) it falls back to Generate
// — identical to the pre-streaming behavior.
func DefaultLLMCallHandler(client LLMClient) Handler {
	return func(ctx context.Context, state *State) error {
		req := LLMRequest{
			System:   state.System,
			Messages: state.Messages,
			Tools:    state.Tools,
		}
		if sink := sinkFrom(ctx); sink != nil {
			if sc, ok := client.(StreamingLLMClient); ok {
				resp, err := sc.GenerateStream(ctx, req, func(ch StreamChunk) error {
					if ch.TextDelta == "" {
						return nil
					}
					return sink(Event{
						Type:      EventTextDelta,
						Iteration: state.Iteration,
						Phase:     PhaseLLMCall,
						TextDelta: ch.TextDelta,
					})
				})
				if err != nil {
					return err
				}
				state.LastResponse = &resp
				state.Usage = state.Usage.Add(resp.Usage)
				return nil
			}
		}
		resp, err := client.Generate(ctx, req)
		if err != nil {
			return err
		}
		state.LastResponse = &resp
		state.Usage = state.Usage.Add(resp.Usage)
		return nil
	}
}

// DefaultPostLLMHandler examines state.LastResponse. If the response has
// pending tool calls, they are copied into state.PendingToolCalls. If it has
// no tool calls, the loop is marked Done with DoneNoToolCalls and the LLM
// content becomes the FinalOutput.
//
// The assistant message itself is appended to state.Messages so the next
// LLM call (if any) sees the prior turn.
func DefaultPostLLMHandler(ctx context.Context, state *State) error {
	resp := state.LastResponse
	if resp == nil {
		// No LLM call happened (e.g. middleware short-circuited). Nothing to do.
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

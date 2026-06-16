package critic

import (
	"context"
	"strings"

	"github.com/farazhassan/gantry"
)

// LLMCritic uses a separate LLMClient to score the last response.
// The rubric is included as the system prompt; the response under review
// is sent as the user message. The critic's reply is parsed for "PASS"
// (case-insensitive) to mean Accept=true; everything else is treated as
// a rejection with the reply text as Reason.
type LLMCritic struct {
	client gantry.LLMClient
	rubric string
}

// NewLLM returns an LLM-driven Critic.
func NewLLM(client gantry.LLMClient, rubric string) *LLMCritic {
	return &LLMCritic{client: client, rubric: rubric}
}

func (c *LLMCritic) Critique(ctx context.Context, state *gantry.State) (Verdict, error) {
	if state.LastResponse == nil {
		return Verdict{Accept: true}, nil
	}
	req := gantry.LLMRequest{
		System: c.rubric,
		Messages: []gantry.Message{
			{Role: gantry.RoleUser, Content: state.LastResponse.Content},
		},
	}
	resp, err := c.client.Generate(ctx, req)
	if err != nil {
		return Verdict{}, err
	}
	verdict := Verdict{Reason: resp.Content}
	if strings.Contains(strings.ToUpper(resp.Content), "PASS") {
		verdict.Accept = true
	}
	return verdict, nil
}

package main

import (
	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/compactor"
	"github.com/farazhassan/gantry/components/humanloop"
	"github.com/farazhassan/gantry/components/limiter"
	"github.com/farazhassan/gantry/components/llm/ollama"
	"github.com/farazhassan/gantry/components/systemprompt"
	"github.com/farazhassan/gantry/components/tool"
)

// newOllamaLLM is the LLM seam: it returns a gantry.LLMClient for the given
// model and endpoint. Swapping in openai/anthropic later is a one-line change
// here.
func newOllamaLLM(model, baseURL string) gantry.LLMClient {
	opts := []ollama.Option{}
	if baseURL != "" {
		opts = append(opts, ollama.WithBaseURL(baseURL))
	}
	return ollama.New(model, opts...)
}

// buildConfig carries the dependencies needed to assemble the agent.
type buildConfig struct {
	LLM       gantry.LLMClient
	Tools     []tool.Tool
	Confirmer humanloop.HumanInLoop

	// SystemPrompt is the agent's base persona/instructions. Empty means no
	// system prompt middleware is installed.
	SystemPrompt string

	// Tuning knobs with sensible zero-value defaults applied in buildAgent.
	MaxIterations int
	MaxTokens     int
	// KeepTurns is how many of the newest turns compaction never touches.
	KeepTurns int
}

// buildAgent assembles the gantry.Agent with the full middleware stack:
// tools (with humanloop confirmation), per-turn budget, and history
// compaction. The LLM seam and tool set are injected so tests can use a
// mock LLM and stub tools. The WithTracer seam is intentionally left
// unwired for a separate effort.
func buildAgent(cfg buildConfig) (*gantry.Agent, error) {
	if cfg.MaxIterations == 0 {
		cfg.MaxIterations = 10
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 100_000
	}
	if cfg.KeepTurns == 0 {
		cfg.KeepTurns = 4
	}

	agent, err := gantry.NewAgent(
		gantry.WithLLM(cfg.LLM),
		gantry.WithMaxIterations(cfg.MaxIterations),
	)
	if err != nil {
		return nil, err
	}

	// Base persona for the assistant, applied during context assembly.
	if err := agent.With(systemprompt.New(cfg.SystemPrompt)); err != nil {
		return nil, err
	}

	// Tools: full-parallel dispatch (parallelism 0).
	if err := agent.With(tool.FromTools(0, cfg.Tools...)); err != nil {
		return nil, err
	}

	// Confirm mutations before any tool executes.
	if cfg.Confirmer != nil {
		if err := agent.With(humanloop.New(cfg.Confirmer)); err != nil {
			return nil, err
		}
	}

	// Per-turn token budget.
	if err := agent.With(limiter.New(limiter.NewBudget(limiter.Limits{MaxTokens: cfg.MaxTokens}))); err != nil {
		return nil, err
	}

	// History compaction: once the prompt reaches 80% of the context window,
	// clear old tool output, truncate huge messages, summarize old turns and,
	// as a last resort, drop them, until it is back to 50%. Every step keeps
	// tool calls with their results. Ollama reports a window only with
	// ollama.WithNumCtx; without one, compaction runs only when the provider
	// rejects a prompt as too long.
	if err := agent.With(compactor.New(
		compactor.NewPolicy(compactor.Policy{Steps: []compactor.Compactor{
			compactor.ClearToolResults(5),
			compactor.TruncateMessages(8000),
			compactor.SummarizeTurns(cfg.LLM, cfg.KeepTurns),
			compactor.DropTurns(cfg.KeepTurns, true),
		}}),
		// MaxTokens here would be a prompt-size target; cfg.MaxTokens is the
		// run's cumulative spend cap (see limiter above), so leave it unset.
		compactor.Budget{},
	)); err != nil {
		return nil, err
	}

	return agent, nil
}

// replyText extracts the assistant's text answer from a finished turn. It is
// used by both the agent tests and the REPL.
func replyText(s *gantry.State) string {
	if s == nil {
		return ""
	}
	if s.FinalOutput != "" {
		return s.FinalOutput
	}
	for i := len(s.Messages) - 1; i >= 0; i-- {
		if s.Messages[i].Role == gantry.RoleAssistant && s.Messages[i].Content != "" {
			return s.Messages[i].Content
		}
	}
	return ""
}

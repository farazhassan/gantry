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
	HistoryHead   int
	HistoryTail   int
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
	if cfg.HistoryHead == 0 {
		cfg.HistoryHead = 4
	}
	if cfg.HistoryTail == 0 {
		cfg.HistoryTail = 30
	}

	agent, err := gantry.NewAgent(
		gantry.WithLLM(cfg.LLM),
		gantry.WithMaxIterations(cfg.MaxIterations),
	)
	if err != nil {
		return nil, err
	}

	// Base persona for the assistant, applied during context assembly.
	systemprompt.WithSystemPrompt(agent, cfg.SystemPrompt)

	// Tools: full-parallel dispatch (parallelism 0).
	tool.WithTools(agent, 0, cfg.Tools...)

	// Confirm mutations before any tool executes.
	if cfg.Confirmer != nil {
		humanloop.WithHumanInLoop(agent, cfg.Confirmer)
	}

	// Per-turn token budget.
	limiter.WithLimiter(agent, limiter.NewBudget(limiter.Limits{MaxTokens: cfg.MaxTokens}))

	// History compaction keeps the first HistoryHead and last HistoryTail
	// messages, dropping the middle. NOTE: HeadTail is a simple strategy — in a
	// very long tool-calling exchange it can drop an assistant message bearing a
	// ToolCall while keeping its ToolResult (or vice-versa), producing a
	// transcript some providers reject. The generous tail makes this unlikely in
	// normal interactive use; a boundary-aware compactor would be the fix if it
	// becomes a problem in practice.
	compactor.WithCompactor(agent,
		compactor.NewHeadTail(cfg.HistoryHead, cfg.HistoryTail),
		compactor.Budget{MaxTokens: cfg.MaxTokens},
	)

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

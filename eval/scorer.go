package eval

import (
	"context"
	"time"

	"github.com/farazhassan/gantry/harness"
)

// RunResult is the output of one agent execution.
type RunResult struct {
	Case        Case
	Config      string
	FinalOutput string
	Trace       *harness.Trace
	Usage       harness.Usage
	Duration    time.Duration
	Err         error
}

// Score is one scorer's verdict for one case/config pair.
type Score struct {
	Name  string
	Value float64 // arbitrary metric; for boolean scorers, 1.0 or 0.0
	Pass  bool
	Notes string
}

// Scorer evaluates a RunResult.
type Scorer interface {
	Name() string
	Score(ctx context.Context, c Case, r RunResult) (Score, error)
}

// ScoredResult is a RunResult with all its Scores attached.
type ScoredResult struct {
	RunResult
	Scores []Score
}

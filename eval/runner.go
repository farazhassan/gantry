package eval

import (
	"context"
	"errors"
	"time"

	"github.com/farazhassan/gantry"
)

// Runner sweeps configs × cases.
type Runner struct {
	Configs     []Config
	Dataset     Dataset
	Scorers     []Scorer
	Parallelism int
	OnResult    func(ScoredResult)
}

// Run executes the entire evaluation and returns the aggregated Report.
// Per-case errors are captured as ScoredResult.Err and do not abort the run.
// Returns an error only for setup failures (missing dataset, configs, etc).
func (r *Runner) Run(ctx context.Context) (Report, error) {
	cases, err := r.Dataset.Cases(ctx)
	if err != nil {
		return Report{}, err
	}
	if len(r.Configs) == 0 {
		return Report{}, errors.New("eval: Runner has no configs")
	}

	type job struct {
		cfg Config
		c   Case
	}
	jobs := make([]job, 0, len(r.Configs)*len(cases))
	for _, cfg := range r.Configs {
		for _, c := range cases {
			jobs = append(jobs, job{cfg: cfg, c: c})
		}
	}

	results := make([]ScoredResult, len(jobs))
	parallelism := r.Parallelism
	if parallelism <= 0 {
		parallelism = 1
	}

	runFns := make([]func(ctx context.Context) error, len(jobs))
	for i, j := range jobs {
		i, j := i, j
		runFns[i] = func(ctx context.Context) error {
			sr := r.executeOne(ctx, j.cfg, j.c)
			results[i] = sr
			if r.OnResult != nil {
				r.OnResult(sr)
			}
			return nil
		}
	}

	if err := gantry.RunParallel(ctx, parallelism, runFns); err != nil {
		return Report{Results: results}, err
	}

	report := Report{Results: results}
	report.Summary = summarize(results)
	return report, nil
}

func (r *Runner) executeOne(ctx context.Context, cfg Config, c Case) ScoredResult {
	start := time.Now()
	rr := RunResult{Case: c, Config: cfg.Name}

	agent, err := cfg.Factory(ctx)
	if err != nil {
		rr.Err = err
		rr.Duration = time.Since(start)
		return ScoredResult{RunResult: rr}
	}
	state, runErr := agent.Run(ctx, c.Input)
	rr.Duration = time.Since(start)
	if state != nil {
		rr.FinalOutput = state.FinalOutput
		rr.Trace = state.Trace
		rr.Usage = state.Usage
	}
	if runErr != nil {
		rr.Err = runErr
		// Recover the partial trace from the wrapped error if state was nil.
		if rr.Trace == nil {
			var tc gantry.TraceCarrier
			if errors.As(runErr, &tc) {
				rr.Trace = tc.Trace()
			}
		}
	}

	scores := make([]Score, 0, len(r.Scorers))
	for _, s := range r.Scorers {
		score, err := s.Score(ctx, c, rr)
		if err != nil {
			score = Score{Name: s.Name(), Pass: false, Notes: "scorer error: " + err.Error()}
		}
		scores = append(scores, score)
	}
	return ScoredResult{RunResult: rr, Scores: scores}
}

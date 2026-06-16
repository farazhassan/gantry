package gantry

import (
	"context"
	"sync"
)

// RunParallel executes jobs concurrently with at most `limit` running at
// once. The function returns when all jobs complete or when ctx is
// cancelled. If any job returns an error, RunParallel returns the first
// non-nil error encountered (but still waits for in-flight jobs to finish).
//
// limit <= 0 is treated as len(jobs) (full parallelism).
func RunParallel(ctx context.Context, limit int, jobs []func(ctx context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(jobs) == 0 {
		return nil
	}
	if limit <= 0 || limit > len(jobs) {
		limit = len(jobs)
	}

	sem := make(chan struct{}, limit)
	var (
		wg       sync.WaitGroup
		errOnce  sync.Once
		firstErr error
	)

	for _, job := range jobs {
		select {
		case <-ctx.Done():
			wg.Wait()
			if firstErr != nil {
				return firstErr
			}
			return ctx.Err()
		case sem <- struct{}{}:
		}
		wg.Add(1)
		j := job
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := j(ctx); err != nil {
				errOnce.Do(func() { firstErr = err })
			}
		}()
	}
	wg.Wait()
	return firstErr
}

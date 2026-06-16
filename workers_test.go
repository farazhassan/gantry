package gantry_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/farazhassan/gantry"
)

func TestRunParallelExecutesAllJobs(t *testing.T) {
	var counter int64
	jobs := make([]func(ctx context.Context) error, 50)
	for i := range jobs {
		jobs[i] = func(ctx context.Context) error {
			atomic.AddInt64(&counter, 1)
			return nil
		}
	}
	if err := gantry.RunParallel(context.Background(), 8, jobs); err != nil {
		t.Fatalf("RunParallel: %v", err)
	}
	if counter != 50 {
		t.Errorf("counter = %d, want 50", counter)
	}
}

func TestRunParallelLimitsConcurrency(t *testing.T) {
	var active, peak int64
	const N = 20
	const limit = 4
	jobs := make([]func(ctx context.Context) error, N)
	for i := range jobs {
		jobs[i] = func(ctx context.Context) error {
			n := atomic.AddInt64(&active, 1)
			for {
				p := atomic.LoadInt64(&peak)
				if n <= p || atomic.CompareAndSwapInt64(&peak, p, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			atomic.AddInt64(&active, -1)
			return nil
		}
	}
	if err := gantry.RunParallel(context.Background(), limit, jobs); err != nil {
		t.Fatalf("RunParallel: %v", err)
	}
	if peak > int64(limit) {
		t.Errorf("peak concurrency = %d, want <= %d", peak, limit)
	}
}

func TestRunParallelReturnsFirstError(t *testing.T) {
	wantErr := errors.New("boom")
	jobs := []func(ctx context.Context) error{
		func(ctx context.Context) error { return nil },
		func(ctx context.Context) error { return wantErr },
		func(ctx context.Context) error { return nil },
	}
	err := gantry.RunParallel(context.Background(), 2, jobs)
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}

func TestRunParallelRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	jobs := []func(ctx context.Context) error{
		func(ctx context.Context) error { return nil },
	}
	err := gantry.RunParallel(ctx, 1, jobs)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

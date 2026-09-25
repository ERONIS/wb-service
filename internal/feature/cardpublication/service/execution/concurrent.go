package execution

import (
	"context"
	"sync"
)

func processConcurrently[T any](
	ctx context.Context,
	items []T,
	limit int,
	process func(T) error,
) error {
	if len(items) == 0 {
		return nil
	}
	if limit < 1 {
		limit = 1
	}
	if limit > len(items) {
		limit = len(items)
	}

	jobs := make(chan T)
	var (
		wait     sync.WaitGroup
		errorMu  sync.Mutex
		firstErr error
	)
	worker := func() {
		defer wait.Done()
		for item := range jobs {
			if ctx.Err() != nil {
				return
			}
			if err := process(item); err != nil {
				errorMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errorMu.Unlock()
			}
		}
	}
	wait.Add(limit)
	for range limit {
		go worker()
	}
	for _, item := range items {
		select {
		case <-ctx.Done():
			close(jobs)
			wait.Wait()
			return ctx.Err()
		case jobs <- item:
		}
	}
	close(jobs)
	wait.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return firstErr
}

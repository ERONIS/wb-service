package transfer_server

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

type processorFunc func(context.Context) error

func (process processorFunc) ProcessPending(ctx context.Context) error {
	return process(ctx)
}

func TestPollingDoesNotBlockIndependentProcessors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	blocked := processorFunc(func(ctx context.Context) error {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return nil
		}
	})
	processed := make(chan struct{}, 1)
	independent := processorFunc(func(context.Context) error {
		select {
		case processed <- struct{}{}:
		default:
		}
		return nil
	})

	done := make(chan error, 1)
	go func() {
		done <- NewPolling(time.Hour, blocked, independent).Run(ctx, nil)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("blocking processor did not start")
	}
	select {
	case <-processed:
	case <-time.After(time.Second):
		t.Fatal("independent processor waited behind blocking processor")
	}
	close(release)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("polling did not stop")
	}
}

func TestPollingWakeTargetsWorkflowProcessor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wake := make(chan struct{}, 1)
	var workflowCalls, downstreamCalls atomic.Int64
	workflowStarted := make(chan struct{}, 2)
	downstreamStarted := make(chan struct{}, 2)
	workflow := processorFunc(func(context.Context) error {
		workflowCalls.Add(1)
		workflowStarted <- struct{}{}
		return nil
	})
	downstream := processorFunc(func(context.Context) error {
		downstreamCalls.Add(1)
		downstreamStarted <- struct{}{}
		return nil
	})
	done := make(chan error, 1)
	go func() {
		done <- NewPollingWithWake(time.Hour, wake, workflow, downstream).Run(ctx, nil)
	}()

	for _, started := range []<-chan struct{}{workflowStarted, downstreamStarted} {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("startup processing did not run")
		}
	}
	wake <- struct{}{}
	select {
	case <-workflowStarted:
	case <-time.After(time.Second):
		t.Fatal("workflow processor did not receive wake")
	}
	if workflowCalls.Load() != 2 || downstreamCalls.Load() != 1 {
		t.Fatalf(
			"unexpected calls after wake: workflow=%d downstream=%d",
			workflowCalls.Load(),
			downstreamCalls.Load(),
		)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("polling did not stop")
	}
}

func TestPollingWarnsOnScheduleDelay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	core, logs := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)

	interval := 10 * time.Millisecond
	var callCount atomic.Int64
	proc := processorFunc(func(ctx context.Context) error {
		call := callCount.Add(1)
		if call == 2 {
			// First tick: sleep longer than ticker interval so next tick is delayed
			time.Sleep(30 * time.Millisecond)
		}
		if call >= 3 {
			cancel()
		}
		return nil
	})

	err := NewPolling(interval, proc).Run(ctx, nil, logger)
	if err != nil {
		t.Fatalf("unexpected polling error: %v", err)
	}

	warnLogs := logs.FilterMessage("Transfer polling schedule delayed").All()
	if len(warnLogs) == 0 {
		t.Fatal("expected at least one schedule delay warning log, got none")
	}
	foundEvent := false
	for _, entry := range warnLogs {
		for _, field := range entry.Context {
			if field.Key == "event" && field.String == "poll_schedule_delay" {
				foundEvent = true
				break
			}
		}
	}
	if !foundEvent {
		t.Fatal("did not find event: poll_schedule_delay in warning log context")
	}
}

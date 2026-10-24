package transfer_server_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	transfer_server "github.com/ERONIS/wb-service/internal/feature/transfer/server"
)

type countingProcessor struct {
	calls atomic.Int64
}

type gatedProgressProcessor struct {
	release   <-chan struct{}
	completed chan<- struct{}
	progress  bool
}

func (processor *countingProcessor) ProcessPending(context.Context) error {
	processor.calls.Add(1)
	return nil
}

func (processor *gatedProgressProcessor) ProcessPending(context.Context) error {
	panic("ожидался вызов с отчётом о прогрессе")
}

func (processor *gatedProgressProcessor) ProcessPendingWithProgress(ctx context.Context) (bool, error) {
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-processor.release:
		close(processor.completed)
		return processor.progress, nil
	}
}

func TestPollingStartupProgressWakesOnlyNextStage(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		progress bool
		calls    int64
	}{
		{name: "без прогресса", progress: false, calls: 1},
		{name: "с прогрессом", progress: true, calls: 2},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			release := make(chan struct{})
			completed := make(chan struct{})
			first := &gatedProgressProcessor{release: release, completed: completed, progress: testCase.progress}
			second := &countingProcessor{}
			third := &countingProcessor{}
			polling := transfer_server.NewPolling(time.Hour, first, second, third)
			done := make(chan error, 1)
			go func() { done <- polling.Run(ctx, nil) }()

			deadline := time.Now().Add(time.Second)
			for second.calls.Load() < 1 || third.calls.Load() < 1 {
				if time.Now().After(deadline) {
					t.Fatal("начальный проход стадий не завершился")
				}
				time.Sleep(time.Millisecond)
			}
			close(release)
			select {
			case <-completed:
			case <-time.After(time.Second):
				t.Fatal("первая стадия не завершилась")
			}
			for second.calls.Load() < testCase.calls {
				if time.Now().After(deadline) {
					t.Fatal("следующая стадия не получила сигнал")
				}
				time.Sleep(time.Millisecond)
			}
			time.Sleep(20 * time.Millisecond)
			if second.calls.Load() != testCase.calls || third.calls.Load() != 1 {
				t.Fatalf("неверное число проходов: вторая=%d третья=%d", second.calls.Load(), third.calls.Load())
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("опрос не завершился после отмены")
			}
		})
	}
}

func TestPollingRunsAgainOnInterval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor := &countingProcessor{}
	done := make(chan error, 1)
	go func() { done <- transfer_server.NewPolling(20*time.Millisecond, processor).Run(ctx, nil) }()
	deadline := time.Now().Add(time.Second)
	for processor.calls.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("повторный проход по таймеру не выполнен")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("опрос не завершился после отмены")
	}
}

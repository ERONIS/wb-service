package transfer_service

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
)

type partialProgressProcessor struct {
	progress bool
	err      error
}

type legacyEmptyProcessor struct{}

type notifyingProcessor struct {
	notified chan struct{}
	release  chan struct{}
}

type cancellingProcessor struct {
	Processor
	cancel context.CancelFunc
}

type cancellingLegacyProcessor struct{ cancel context.CancelFunc }

func (p partialProgressProcessor) ProcessPending(context.Context) error { return p.err }
func (p partialProgressProcessor) ProcessPendingWithProgress(context.Context) (bool, error) {
	return p.progress, p.err
}

func (legacyEmptyProcessor) ProcessPending(context.Context) error { return nil }

func (p notifyingProcessor) ProcessPending(context.Context) error {
	panic("неожиданный старый интерфейс")
}
func (p notifyingProcessor) ProcessPendingWithNotify(ctx context.Context, notify func()) error {
	notify()
	close(p.notified)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.release:
		return errors.New("частичная ошибка")
	}
}

func TestPollingPartialProgressAndEmptyLegacy(t *testing.T) {
	for _, tc := range []struct {
		name          string
		processor     Processor
		wakes, errors int
	}{
		{"partial success", partialProgressProcessor{true, errors.New("частичная ошибка")}, 1, 1},
		{"error without progress", partialProgressProcessor{false, errors.New("ошибка")}, 0, 1},
		{"empty legacy", legacyEmptyProcessor{}, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wakes, reported := 0, 0
			p := NewPolling(time.Hour, tc.processor)
			// Завершаем контекст из процессора после первого вызова, не ожидая тикера.
			wrapped := cancellingProcessor{Processor: tc.processor, cancel: cancel}
			p.runProcessor(ctx, zap.NewNop(), 0, wrapped, nil, func() { wakes++ }, func(error) { reported++ })
			if wakes != tc.wakes || reported != tc.errors {
				t.Fatalf("wake=%d errors=%d", wakes, reported)
			}
		})
	}
}

func (p cancellingProcessor) ProcessPendingWithProgress(ctx context.Context) (bool, error) {
	defer p.cancel()
	if aware, ok := p.Processor.(ProgressAwareProcessor); ok {
		return aware.ProcessPendingWithProgress(ctx)
	}
	return false, p.Processor.ProcessPending(ctx)
}

func TestPollingNotifiesBeforeProcessorReturns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor := notifyingProcessor{make(chan struct{}), make(chan struct{})}
	p := NewPolling(time.Hour, processor)
	wakes := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.runProcessor(ctx, zap.NewNop(), 0, processor, nil, func() { wakes <- struct{}{} }, func(error) { cancel() })
	}()
	select {
	case <-processor.notified:
	case <-time.After(time.Second):
		t.Fatal("уведомление задержано")
	}
	select {
	case <-wakes:
	default:
		t.Fatal("следующий этап не разбужен")
	}
	close(processor.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("процессор не завершился")
	}
}

func (p cancellingLegacyProcessor) ProcessPending(context.Context) error { p.cancel(); return nil }

func TestPollingLegacyDoesNotReportProgress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor := cancellingLegacyProcessor{cancel}
	wakes := 0
	NewPolling(time.Hour, processor).runProcessor(ctx, zap.NewNop(), 0, processor, nil,
		func() { wakes++ }, func(err error) { t.Fatal(err) })
	if wakes != 0 {
		t.Fatal("пустой обход запустил каскад")
	}
}

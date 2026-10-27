package polling

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
)

type notifyingProcessor struct {
	notified chan struct{}
	release  chan struct{}
}

type cancellingLegacyProcessor struct{ cancel context.CancelFunc }

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

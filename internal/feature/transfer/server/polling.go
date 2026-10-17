package transfer_server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	core_logger "github.com/ERONIS/wb-service/internal/core/logger"
	"go.uber.org/zap"
)

type Processor interface {
	ProcessPending(ctx context.Context) error
}

// ProgressAwareProcessor позволяет процессору сигнализировать диспетчеру опроса о наличии реального продвижения.
type ProgressAwareProcessor interface {
	ProcessPendingWithProgress(ctx context.Context) (progress bool, err error)
}

// ProgressNotifyingProcessor сообщает о продвижении сразу после фиксации порции работы.
type ProgressNotifyingProcessor interface {
	ProcessPendingWithNotify(context.Context, func()) error
}

type ErrorHandler func(error)

type Polling struct {
	interval   time.Duration
	wake       <-chan struct{}
	processors []Processor
}

func NewPolling(interval time.Duration, processors ...Processor) *Polling {
	if interval <= 0 {
		panic("transfer polling interval must be positive")
	}
	if len(processors) == 0 {
		panic("transfer polling processors are empty")
	}
	clone := append([]Processor(nil), processors...)
	for _, processor := range clone {
		if processor == nil {
			panic("transfer polling processor is nil")
		}
	}
	return &Polling{interval: interval, processors: clone}
}

func NewPollingWithWake(
	interval time.Duration,
	wake <-chan struct{},
	processors ...Processor,
) *Polling {
	polling := NewPolling(interval, processors...)
	if wake == nil {
		panic("transfer polling wake channel is nil")
	}
	polling.wake = wake
	return polling
}

func (polling *Polling) Run(ctx context.Context, onError ErrorHandler, loggers ...*zap.Logger) error {
	if ctx == nil {
		return errors.New("run transfer polling: context is nil")
	}
	logger := core_logger.Fallback(loggers...)
	var errorMu sync.Mutex
	reportError := func(err error) {
		if err == nil || ctx.Err() != nil || onError == nil {
			return
		}
		errorMu.Lock()
		defer errorMu.Unlock()
		onError(err)
	}

	stageWakes := make([]chan struct{}, len(polling.processors))
	for i := range stageWakes {
		stageWakes[i] = make(chan struct{}, 1)
	}

	if polling.wake != nil {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-polling.wake:
					if !ok {
						return
					}
					select {
					case stageWakes[0] <- struct{}{}:
					default:
					}
				}
			}
		}()
	}

	wakeDownstream := func(currentIndex int) {
		for next := currentIndex + 1; next < len(stageWakes); next++ {
			select {
			case stageWakes[next] <- struct{}{}:
			default:
			}
		}
	}

	var wait sync.WaitGroup
	for index, processor := range polling.processors {
		index, processor := index, processor
		wait.Add(1)
		go func() {
			defer wait.Done()
			polling.runProcessor(
				ctx,
				logger,
				index,
				processor,
				stageWakes[index],
				func() { wakeDownstream(index) },
				reportError,
			)
		}()
	}
	<-ctx.Done()
	wait.Wait()
	return nil
}

func (polling *Polling) runProcessor(
	ctx context.Context,
	logger *zap.Logger,
	index int,
	processor Processor,
	wake <-chan struct{},
	onSuccess func(),
	reportError ErrorHandler,
) {
	var cycle int64
	process := func(trigger string, scheduledAt time.Time) {
		cycle++
		startedAt := time.Now()
		scheduleDelay := startedAt.Sub(scheduledAt)
		if scheduleDelay < 0 {
			scheduleDelay = 0
		}
		fields := []zap.Field{
			zap.Int64("poll_cycle", cycle),
			zap.String("trigger", trigger),
			zap.String("processor", fmt.Sprintf("%T", processor)),
			zap.Int("processor_index", index),
		}
		processorLogger := logger.With(fields...)
		if scheduleDelay > polling.interval {
			processorLogger.Warn(
				"Transfer polling schedule delayed",
				zap.String("event", "poll_schedule_delay"),
				zap.Duration("schedule_delay", scheduleDelay),
				zap.Duration("poll_interval", polling.interval),
			)
		}
		var progress bool
		var err error
		if notifier, ok := processor.(ProgressNotifyingProcessor); ok {
			err = notifier.ProcessPendingWithNotify(ctx, onSuccess)
		} else if pap, ok := processor.(ProgressAwareProcessor); ok {
			progress, err = pap.ProcessPendingWithProgress(ctx)
		} else {
			err = processor.ProcessPending(ctx)
		}
		if err != nil {
			reportError(err)
		}
		if progress && onSuccess != nil {
			onSuccess()
		}
	}

	if ctx.Err() != nil {
		return
	}
	process("startup", time.Now())
	ticker := time.NewTicker(polling.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
			process("wake", time.Now())
		case scheduledAt := <-ticker.C:
			process("tick", scheduledAt)
		}
	}
}

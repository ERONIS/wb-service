package transfer_server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ERONIS/wb-service/internal/core/observability"
	"go.uber.org/zap"
)

type Processor interface {
	ProcessPending(ctx context.Context) error
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
	logger := observability.Logger(loggers...)
	var errorMu sync.Mutex
	reportError := func(err error) {
		if err == nil || ctx.Err() != nil || onError == nil {
			return
		}
		// Error handlers were called serially by the old polling loop. Preserve
		// that contract while the processors themselves run independently.
		errorMu.Lock()
		defer errorMu.Unlock()
		onError(err)
	}

	var wait sync.WaitGroup
	for index, processor := range polling.processors {
		index, processor := index, processor
		wait.Add(1)
		go func() {
			defer wait.Done()
			var wake <-chan struct{}
			if index == 0 {
				// A finalized batch only needs to wake the workflow/discovery
				// processor. Downstream processors observe its committed state on
				// their own ticks and never wait behind its work.
				wake = polling.wake
			}
			polling.runProcessor(ctx, logger, index, processor, wake, reportError)
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
		processorLogger.Debug("Transfer polling processor started")
		err := processor.ProcessPending(ctx)
		observability.LogTimingDebug(
			processorLogger,
			"transfer",
			"poll_processor",
			startedAt,
			err,
			zap.Duration("poll_interval", polling.interval),
			zap.Duration("schedule_delay", scheduleDelay),
		)
		observability.LogTimingDebug(
			processorLogger,
			"transfer",
			"poll_cycle",
			startedAt,
			err,
			zap.Int("processors_count", len(polling.processors)),
			zap.Duration("poll_interval", polling.interval),
			zap.Duration("schedule_delay", scheduleDelay),
			zap.Bool("interval_exceeded", time.Since(startedAt) > polling.interval),
		)
		if err != nil {
			reportError(err)
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

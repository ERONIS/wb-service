package transfer_server

import (
	"context"
	"errors"
	"time"
)

type Processor interface {
	ProcessPending(ctx context.Context) error
}

type ErrorHandler func(error)

type Polling struct {
	interval   time.Duration
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

func (polling *Polling) Run(ctx context.Context, onError ErrorHandler) error {
	if ctx == nil {
		return errors.New("run transfer polling: context is nil")
	}
	process := func() {
		for _, processor := range polling.processors {
			if err := processor.ProcessPending(ctx); err != nil {
				if ctx.Err() == nil && onError != nil {
					onError(err)
				}
			}
		}
	}

	process()
	ticker := time.NewTicker(polling.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			process()
		}
	}
}

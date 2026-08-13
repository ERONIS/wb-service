package platform_outbox

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

type Handler interface {
	EventType() string
	Handle(ctx context.Context, event ClaimedEvent) error
}

type HandlerFunc struct {
	Type string
	Func func(context.Context, ClaimedEvent) error
}

func (handler HandlerFunc) EventType() string {
	return handler.Type
}

func (handler HandlerFunc) Handle(
	ctx context.Context,
	event ClaimedEvent,
) error {
	if handler.Func == nil {
		return PermanentDeliveryError(
			"handler_not_configured",
			errors.New("platform outbox handler function is nil"),
		)
	}
	return handler.Func(ctx, event)
}

type DispatcherConfig struct {
	Owner            string
	PollInterval     time.Duration
	LeaseDuration    time.Duration
	HandleTimeout    time.Duration
	OperationTimeout time.Duration
	BaseRetryDelay   time.Duration
	MaxRetryDelay    time.Duration
	MaxAttempts      int
}

func DefaultDispatcherConfig(owner string) DispatcherConfig {
	return DispatcherConfig{
		Owner:            owner,
		PollInterval:     time.Second,
		LeaseDuration:    30 * time.Second,
		HandleTimeout:    20 * time.Second,
		OperationTimeout: 5 * time.Second,
		BaseRetryDelay:   time.Second,
		MaxRetryDelay:    5 * time.Minute,
		MaxAttempts:      10,
	}
}

func (config DispatcherConfig) validate() error {
	if err := validateOwner(config.Owner); err != nil {
		return err
	}
	switch {
	case config.PollInterval <= 0:
		return errors.New("outbox poll interval must be positive")
	case config.LeaseDuration <= 0:
		return errors.New("outbox lease duration must be positive")
	case config.HandleTimeout <= 0:
		return errors.New("outbox handle timeout must be positive")
	case config.OperationTimeout <= 0:
		return errors.New("outbox operation timeout must be positive")
	case config.HandleTimeout >= config.LeaseDuration:
		return errors.New("outbox handle timeout must be shorter than lease")
	case config.BaseRetryDelay <= 0:
		return errors.New("outbox base retry delay must be positive")
	case config.MaxRetryDelay < config.BaseRetryDelay:
		return errors.New("outbox max retry delay is shorter than base delay")
	case config.MaxAttempts <= 0:
		return errors.New("outbox max attempts must be positive")
	default:
		return nil
	}
}

type Dispatcher struct {
	store      DeliveryStore
	config     DispatcherConfig
	handlers   map[string]Handler
	eventTypes []string
	now        func() time.Time
}

func NewDispatcher(
	store DeliveryStore,
	config DispatcherConfig,
	handlers ...Handler,
) (*Dispatcher, error) {
	if store == nil {
		return nil, errors.New("platform outbox delivery store is nil")
	}
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("invalid platform outbox dispatcher config: %w", err)
	}
	if len(handlers) == 0 {
		return nil, errors.New("platform outbox handlers are empty")
	}

	byType := make(map[string]Handler, len(handlers))
	eventTypes := make([]string, 0, len(handlers))
	for _, handler := range handlers {
		if handler == nil {
			return nil, errors.New("platform outbox handler is nil")
		}
		eventType := handler.EventType()
		if err := validateEventType(eventType); err != nil {
			return nil, fmt.Errorf("invalid platform outbox handler: %w", err)
		}
		if _, exists := byType[eventType]; exists {
			return nil, fmt.Errorf(
				"duplicate platform outbox handler for event type %q",
				eventType,
			)
		}
		byType[eventType] = handler
		eventTypes = append(eventTypes, eventType)
	}
	sort.Strings(eventTypes)

	return &Dispatcher{
		store:      store,
		config:     config,
		handlers:   byType,
		eventTypes: eventTypes,
		now:        time.Now,
	}, nil
}

func (dispatcher *Dispatcher) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("run platform outbox dispatcher: context is nil")
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		claimed, found, err := dispatcher.claim(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("run platform outbox dispatcher: %w", err)
		}
		if !found {
			if err := waitForPoll(ctx, dispatcher.config.PollInterval); err != nil {
				return nil
			}
			continue
		}

		if err := dispatcher.deliver(ctx, claimed); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf(
				"deliver platform outbox event ID=%q: %w",
				claimed.ID,
				err,
			)
		}
	}
}

func (dispatcher *Dispatcher) claim(
	ctx context.Context,
) (ClaimedEvent, bool, error) {
	operationContext, cancel := context.WithTimeout(
		ctx,
		dispatcher.config.OperationTimeout,
	)
	defer cancel()

	return dispatcher.store.Claim(
		operationContext,
		dispatcher.eventTypes,
		dispatcher.config.Owner,
		dispatcher.now().UTC(),
		dispatcher.config.LeaseDuration,
	)
}

func (dispatcher *Dispatcher) deliver(
	ctx context.Context,
	claimed ClaimedEvent,
) error {
	handler, exists := dispatcher.handlers[claimed.Type]
	if !exists {
		return errors.New("claimed event has no registered handler")
	}

	handleContext, cancel := context.WithTimeout(
		ctx,
		dispatcher.config.HandleTimeout,
	)
	handleErr := handler.Handle(handleContext, claimed)
	handleContextErr := handleContext.Err()
	cancel()
	if ctx.Err() != nil {
		return nil
	}

	now := dispatcher.now().UTC()
	if handleErr == nil && handleContextErr == nil {
		return dispatcher.complete(ctx, claimed.Lease, now)
	}
	if handleErr == nil {
		handleErr = handleContextErr
	}
	failure := classifyDeliveryError(handleErr)
	if claimed.Attempts >= dispatcher.config.MaxAttempts || failure.permanent {
		return dispatcher.deadLetter(
			ctx,
			claimed.Lease,
			now,
			failure.safeCode,
		)
	}

	delay := dispatcher.retryDelay(claimed.Attempts)
	if failure.retryAfter > delay {
		delay = failure.retryAfter
	}
	return dispatcher.reschedule(
		ctx,
		claimed.Lease,
		now,
		now.Add(delay),
		failure.safeCode,
	)
}

func (dispatcher *Dispatcher) complete(
	ctx context.Context,
	lease Lease,
	now time.Time,
) error {
	operationContext, cancel := context.WithTimeout(
		ctx,
		dispatcher.config.OperationTimeout,
	)
	defer cancel()
	return dispatcher.store.Complete(operationContext, lease, now)
}

func (dispatcher *Dispatcher) reschedule(
	ctx context.Context,
	lease Lease,
	now time.Time,
	availableAt time.Time,
	safeCode string,
) error {
	operationContext, cancel := context.WithTimeout(
		ctx,
		dispatcher.config.OperationTimeout,
	)
	defer cancel()
	return dispatcher.store.Reschedule(
		operationContext,
		lease,
		now,
		availableAt,
		safeCode,
	)
}

func (dispatcher *Dispatcher) deadLetter(
	ctx context.Context,
	lease Lease,
	now time.Time,
	safeCode string,
) error {
	operationContext, cancel := context.WithTimeout(
		ctx,
		dispatcher.config.OperationTimeout,
	)
	defer cancel()
	return dispatcher.store.DeadLetter(
		operationContext,
		lease,
		now,
		safeCode,
	)
}

func (dispatcher *Dispatcher) retryDelay(attempt int) time.Duration {
	delay := dispatcher.config.BaseRetryDelay
	for current := 1; current < attempt; current++ {
		if delay >= dispatcher.config.MaxRetryDelay/2 {
			return dispatcher.config.MaxRetryDelay
		}
		delay *= 2
	}
	if delay > dispatcher.config.MaxRetryDelay {
		return dispatcher.config.MaxRetryDelay
	}
	return delay
}

func waitForPoll(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type deliveryError struct {
	err        error
	safeCode   string
	permanent  bool
	retryAfter time.Duration
}

func (err *deliveryError) Error() string {
	return err.err.Error()
}

func (err *deliveryError) Unwrap() error {
	return err.err
}

func PermanentDeliveryError(safeCode string, err error) error {
	return newDeliveryError(safeCode, true, 0, err)
}

func RetryableDeliveryError(
	safeCode string,
	retryAfter time.Duration,
	err error,
) error {
	return newDeliveryError(safeCode, false, retryAfter, err)
}

func newDeliveryError(
	safeCode string,
	permanent bool,
	retryAfter time.Duration,
	err error,
) error {
	if err == nil {
		err = errors.New("outbox delivery failed")
	}
	if validateErr := validateSafeCode(safeCode); validateErr != nil {
		panic(fmt.Sprintf("invalid outbox safe error code: %v", validateErr))
	}
	if retryAfter < 0 {
		panic("outbox retry-after duration is negative")
	}

	return &deliveryError{
		err:        err,
		safeCode:   safeCode,
		permanent:  permanent,
		retryAfter: retryAfter,
	}
}

func classifyDeliveryError(err error) deliveryError {
	var classified *deliveryError
	if errors.As(err, &classified) {
		return *classified
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return deliveryError{
			err:      err,
			safeCode: "handler_timeout",
		}
	}

	safeCode := "handler_failed"
	if err == nil {
		err = errors.New("outbox handler failed without an error")
	}
	return deliveryError{
		err:      err,
		safeCode: safeCode,
	}
}

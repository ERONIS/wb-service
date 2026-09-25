package client

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
	transport "github.com/ERONIS/wb-service/internal/core/transport/wb/transport"
	"go.uber.org/zap"
)

var requestTraceSequence atomic.Uint64

type attemptSummary struct {
	statusCode int
}

type requestTrace struct {
	requestID       string
	cabinetID       config.CabinetID
	cabinetName     string
	operation       policy.OperationID
	method          string
	path            string
	bucketID        policy.BucketID
	startedAt       time.Time
	requestBytes    int
	responseBytes   int
	limiterWait     time.Duration
	statusCode      int
	delivery        DeliveryState
	errorCode       string
	err             error
	observationErrs int
	maxAttempts     int
	attemptCount    int
	dispatchCount   int
	attempts        []attemptSummary
}

type attemptRecorder struct {
	mutex  sync.Mutex
	result transport.AttemptResult
}

func newRequestTrace(
	client *APIClient,
	operation policy.Operation,
	maxAttempts int,
) *requestTrace {
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	startedAt := time.Now()
	trace := &requestTrace{
		requestID:   newRequestID(startedAt),
		operation:   operation.ID(),
		method:      operation.Method(),
		path:        operation.Path(),
		bucketID:    operation.BucketID(),
		startedAt:   startedAt,
		maxAttempts: maxAttempts,
		attempts: make(
			[]attemptSummary,
			0,
			maxAttempts,
		),
	}
	if client != nil {
		trace.cabinetID = client.cabinetID
		trace.cabinetName = client.cabinetName
	}

	return trace
}

func newRequestID(startedAt time.Time) string {
	return fmt.Sprintf(
		"%016x-%016x",
		uint64(startedAt.UnixNano()),
		requestTraceSequence.Add(1),
	)
}

func (recorder *attemptRecorder) RecordAttempt(
	result transport.AttemptResult,
) {
	if recorder == nil {
		return
	}

	recorder.mutex.Lock()
	recorder.result = result
	recorder.mutex.Unlock()
}

func (recorder *attemptRecorder) snapshot(
	response *http.Response,
) transport.AttemptResult {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()

	result := recorder.result
	if response != nil {
		result.StatusCode = response.StatusCode
	}

	return result
}

func (trace *requestTrace) recordAttempt(
	result transport.AttemptResult,
) {
	if trace == nil {
		return
	}

	trace.attemptCount++
	trace.dispatchCount += result.DispatchCount
	if len(trace.attempts) >= trace.maxAttempts {
		return
	}

	trace.attempts = append(trace.attempts, attemptSummary{
		statusCode: result.StatusCode,
	})
}

func (trace *requestTrace) addLimiterWait(duration time.Duration) {
	if trace == nil || duration <= 0 {
		return
	}

	trace.limiterWait += duration
}

func (trace *requestTrace) addResponseBytes(responseBytes int) {
	if trace == nil || responseBytes <= 0 {
		return
	}

	trace.responseBytes += responseBytes
}

func (trace *requestTrace) recordObservationError(err error) {
	if trace == nil || err == nil {
		return
	}

	trace.observationErrs++
}

func (trace *requestTrace) complete(
	result executionResult,
	executeErr error,
) {
	if trace == nil {
		return
	}

	trace.statusCode = result.statusCode
	trace.delivery = result.delivery
	trace.errorCode = classifiedErrorCode(executeErr)
	trace.err = executeErr
}

const slowLimiterWaitLogThreshold = time.Second

func (trace *requestTrace) log(logger *zap.Logger) {
	if trace == nil || logger == nil {
		return
	}

	fields := []zap.Field{
		zap.String("event", "wb_request_completed"),
		zap.String("request_id", trace.requestID),
		zap.String("cabinet_id", string(trace.cabinetID)),
		zap.String("cabinet_name", trace.cabinetName),
		zap.String("operation", trace.operation.String()),
		zap.String("method", trace.method),
		zap.String("path", trace.path),
		zap.String("bucket_id", trace.bucketID.String()),
		zap.Int("attempt_count", trace.attemptCount),
		zap.Ints("attempt_statuses", trace.attemptStatuses()),
		zap.Int("dispatch_count", trace.dispatchCount),
		zap.Duration("limiter_wait", trace.limiterWait),
		zap.Int("request_bytes", trace.requestBytes),
		zap.Int("response_bytes", trace.responseBytes),
		zap.Duration("duration", time.Since(trace.startedAt)),
		zap.Int("status_code", trace.statusCode),
		zap.String("delivery_state", trace.delivery.String()),
		zap.String("result", trace.resultLabel()),
		zap.String("error_code", trace.errorCode),
		zap.Int("rate_limit_observation_errors", trace.observationErrs),
	}
	if trace.err != nil {
		fields = append(fields, zap.NamedError("error", trace.err))
	}
	if trace.errorCode != "" {
		logger.Warn("WB request completed", fields...)
		return
	}
	if trace.limiterWait >= slowLimiterWaitLogThreshold {
		logger.Info("WB request completed", fields...)
		return
	}
	logger.Debug("WB request completed", fields...)
}

func (trace *requestTrace) attemptStatuses() []int {
	statuses := make([]int, len(trace.attempts))
	for index, attempt := range trace.attempts {
		statuses[index] = attempt.statusCode
	}

	return statuses
}

func (trace *requestTrace) resultLabel() string {
	if trace.errorCode == "" {
		return "success"
	}

	return "error"
}

func clientLogger(client *APIClient) *zap.Logger {
	if client == nil {
		return nil
	}

	return client.logger
}

var _ transport.AttemptRecorder = (*attemptRecorder)(nil)

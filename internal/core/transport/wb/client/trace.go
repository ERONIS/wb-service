package client

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
	"go.uber.org/zap"
)

const slowLimiterWaitLogThreshold = time.Second

var requestTraceSequence atomic.Uint64

type requestTrace struct {
	requestID       string
	sellerID        domain.SellerID
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
	dispatchCount   int
	attempts        []int
}

func newRequestTrace(
	client *APIClient,
	operation policy.Operation,
	maxAttempts int,
) *requestTrace {
	startedAt := time.Now()
	trace := &requestTrace{
		requestID: newRequestID(startedAt),
		operation: operation.ID(),
		method:    operation.Method(),
		path:      operation.Path(),
		bucketID:  operation.BucketID(),
		startedAt: startedAt,
		attempts:  make([]int, 0, maxAttempts),
	}
	if client != nil {
		trace.sellerID = client.sellerID
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

func (trace *requestTrace) recordAttempt(
	statusCode int,
	dispatchCount int,
) {
	trace.dispatchCount += dispatchCount
	trace.attempts = append(trace.attempts, statusCode)
}

func (trace *requestTrace) addLimiterWait(duration time.Duration) {
	trace.limiterWait += duration
}

func (trace *requestTrace) addResponseBytes(responseBytes int) {
	trace.responseBytes += responseBytes
}

func (trace *requestTrace) recordObservationError(err error) {
	if err == nil {
		return
	}

	trace.observationErrs++
}

func (trace *requestTrace) complete(
	result executionResult,
	executeErr error,
) {
	trace.statusCode = result.statusCode
	trace.delivery = result.delivery
	trace.errorCode = classifiedErrorCode(executeErr)
	trace.err = executeErr
}

func (trace *requestTrace) log(logger *zap.Logger) {
	if logger == nil {
		return
	}

	fields := []zap.Field{
		zap.String("event", "wb_request_completed"),
		zap.String("request_id", trace.requestID),
		zap.String("seller_id", string(trace.sellerID)),
		zap.String("cabinet_name", trace.cabinetName),
		zap.String("operation", trace.operation.String()),
		zap.String("method", trace.method),
		zap.String("path", trace.path),
		zap.String("bucket_id", trace.bucketID.String()),
		zap.Int("attempt_count", len(trace.attempts)),
		zap.Ints("attempt_statuses", trace.attempts),
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

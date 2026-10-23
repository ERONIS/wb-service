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

// requestTrace собирает данные об отправке для одной записи в журнале.
type requestTrace struct {
	details requestTraceDetails
	metrics requestTraceMetrics
}

// requestTraceDetails описывает запрос, к которому относится запись в журнале.
type requestTraceDetails struct {
	requestID   string
	sellerID    domain.SellerID
	cabinetName string
	operation   policy.OperationID
	method      string
	path        string
	bucketID    policy.BucketID
	startedAt   time.Time
}

// requestTraceMetrics накапливает данные обо всех попытках запроса.
type requestTraceMetrics struct {
	requestBytes    int
	responseBytes   int
	limiterWait     time.Duration
	observationErrs int
	observationErr  error
	dispatchCount   int
	attempts        []int
}

// newRequestTrace начинает учёт логического запроса и его попыток.
func newRequestTrace(
	client *APIClient,
	operation policy.Operation,
	maxAttempts int,
) *requestTrace {
	startedAt := time.Now()
	trace := &requestTrace{
		details: requestTraceDetails{
			requestID: newRequestID(startedAt),
			operation: operation.ID(),
			method:    operation.Method(),
			path:      operation.Path(),
			bucketID:  operation.BucketID(),
			startedAt: startedAt,
		},
		metrics: requestTraceMetrics{
			attempts: make([]int, 0, maxAttempts),
		},
	}
	if client != nil {
		trace.details.sellerID = client.sellerID
		trace.details.cabinetName = client.cabinetName
	}

	return trace
}

// log записывает единый итог запроса с подходящим уровнем журнала.
func (trace *requestTrace) log(logger *zap.Logger, result executionResult, executeErr error) {
	if logger == nil {
		return
	}
	errorCode := classifiedErrorCode(executeErr)
	resultLabel := "success"
	if errorCode != "" {
		resultLabel = "error"
	}

	fields := []zap.Field{
		zap.String("event", "wb_request_completed"),
		zap.String("request_id", trace.details.requestID),
		zap.String("seller_id", string(trace.details.sellerID)),
		zap.String("cabinet_name", trace.details.cabinetName),
		zap.String("operation", trace.details.operation.String()),
		zap.String("method", trace.details.method),
		zap.String("path", trace.details.path),
		zap.String("bucket_id", trace.details.bucketID.String()),
		zap.Int("attempt_count", len(trace.metrics.attempts)),
		zap.Ints("attempt_statuses", trace.metrics.attempts),
		zap.Int("dispatch_count", trace.metrics.dispatchCount),
		zap.Duration("limiter_wait", trace.metrics.limiterWait),
		zap.Int("request_bytes", trace.metrics.requestBytes),
		zap.Int("response_bytes", trace.metrics.responseBytes),
		zap.Duration("duration", time.Since(trace.details.startedAt)),
		zap.Int("status_code", result.statusCode),
		zap.String("delivery_state", result.delivery.String()),
		zap.String("result", resultLabel),
		zap.String("error_code", errorCode),
		zap.Int("rate_limit_observation_errors", trace.metrics.observationErrs),
	}
	if executeErr != nil {
		fields = append(fields, zap.NamedError("error", executeErr))
	}
	if trace.metrics.observationErr != nil {
		fields = append(fields, zap.NamedError("rate_limit_observation_error", trace.metrics.observationErr))
	}
	if errorCode == ErrorCodeCanceled && trace.metrics.observationErr == nil {
		logger.Debug("WB request completed", fields...)
		return
	}
	if errorCode != "" || trace.metrics.observationErr != nil {
		logger.Warn("WB request completed", fields...)
		return
	}
	if trace.metrics.limiterWait >= slowLimiterWaitLogThreshold {
		logger.Info("WB request completed", fields...)
		return
	}
	logger.Debug("WB request completed", fields...)
}

// recordAttempt запоминает статус и число фактических отправок попытки.
func (trace *requestTrace) recordAttempt(
	statusCode int,
	dispatchCount int,
) {
	trace.metrics.dispatchCount += dispatchCount
	trace.metrics.attempts = append(trace.metrics.attempts, statusCode)
}

// addLimiterWait накапливает время ожидания разрешения от лимитера.
func (trace *requestTrace) addLimiterWait(duration time.Duration) {
	trace.metrics.limiterWait += duration
}

// addResponseBytes накапливает число прочитанных байтов ответа.
func (trace *requestTrace) addResponseBytes(responseBytes int) {
	trace.metrics.responseBytes += responseBytes
}

// recordObservationError учитывает ошибки чтения заголовков лимита.
func (trace *requestTrace) recordObservationError(err error) {
	if err == nil {
		return
	}

	trace.metrics.observationErrs++
	if trace.metrics.observationErr == nil {
		trace.metrics.observationErr = err
	}
}

// newRequestID создаёт идентификатор из времени и порядкового номера.
func newRequestID(startedAt time.Time) string {
	return fmt.Sprintf(
		"%016x-%016x",
		uint64(startedAt.UnixNano()),
		requestTraceSequence.Add(1),
	)
}

// clientLogger возвращает журнал клиента, если клиент существует.
func clientLogger(client *APIClient) *zap.Logger {
	if client == nil {
		return nil
	}

	return client.logger
}

package core_postgres_pool

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// slowQueryThreshold определяет порог длительности запроса для предупреждения в логах.
const slowQueryThreshold = 500 * time.Millisecond

type queryTraceKey struct{}
type copyTraceKey struct{}
type acquireTraceKey struct{}

type queryTraceData struct {
	startTime time.Time
	operation string
}

type copyTraceData struct {
	startTime time.Time
	tableName string
}

type acquireTraceData struct {
	startTime time.Time
}

// queryTracer регистрирует SQL, CopyFrom и ожидание соединения без текста ошибок и аргументов.
type queryTracer struct {
	logger *zap.Logger
}

func newQueryTracer(logger *zap.Logger) *queryTracer {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &queryTracer{logger: logger}
}

// TraceQueryStart вызывается в начале выполнения Query, QueryRow и Exec.
func (t *queryTracer) TraceQueryStart(
	ctx context.Context,
	conn *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	op := extractOperation(data.SQL)
	return context.WithValue(ctx, queryTraceKey{}, queryTraceData{
		startTime: time.Now(),
		operation: op,
	})
}

// TraceQueryEnd вызывается при завершении Query, QueryRow и Exec.
func (t *queryTracer) TraceQueryEnd(
	ctx context.Context,
	conn *pgx.Conn,
	data pgx.TraceQueryEndData,
) {
	val := ctx.Value(queryTraceKey{})
	if val == nil {
		return
	}
	traceData, ok := val.(queryTraceData)
	if !ok {
		return
	}

	duration := time.Since(traceData.startTime)
	fields := []zap.Field{
		zap.String("op", traceData.operation),
		zap.Duration("duration", duration),
	}

	if data.CommandTag.RowsAffected() > 0 {
		fields = append(fields, zap.Int64("rows_affected", data.CommandTag.RowsAffected()))
	}

	if data.Err != nil && !errors.Is(data.Err, pgx.ErrNoRows) {
		var pgErr *pgconn.PgError
		if errors.As(data.Err, &pgErr) {
			fields = append(fields, zap.String("sqlstate", pgErr.Code))
		}
		fields = append(fields, zap.String("error_kind", safeErrorKind(data.Err)))
		t.logger.Warn("ошибка выполнения SQL запроса", fields...)
		return
	}

	if duration > slowQueryThreshold {
		t.logger.Warn("медленный SQL запрос", fields...)
		return
	}

	t.logger.Debug("SQL запрос выполнен", fields...)
}

// TraceCopyFromStart вызывается в начале операции CopyFrom.
func (t *queryTracer) TraceCopyFromStart(
	ctx context.Context,
	conn *pgx.Conn,
	data pgx.TraceCopyFromStartData,
) context.Context {
	table := strings.Join(data.TableName, ".")
	return context.WithValue(ctx, copyTraceKey{}, copyTraceData{
		startTime: time.Now(),
		tableName: table,
	})
}

// TraceCopyFromEnd вызывается по завершении операции CopyFrom.
func (t *queryTracer) TraceCopyFromEnd(
	ctx context.Context,
	conn *pgx.Conn,
	data pgx.TraceCopyFromEndData,
) {
	val := ctx.Value(copyTraceKey{})
	if val == nil {
		return
	}
	traceData, ok := val.(copyTraceData)
	if !ok {
		return
	}

	duration := time.Since(traceData.startTime)
	fields := []zap.Field{
		zap.String("table", traceData.tableName),
		zap.Duration("duration", duration),
		zap.Int64("rows_copied", data.CommandTag.RowsAffected()),
	}

	if data.Err != nil {
		var pgErr *pgconn.PgError
		if errors.As(data.Err, &pgErr) {
			fields = append(fields, zap.String("sqlstate", pgErr.Code))
		}
		fields = append(fields, zap.String("error_kind", safeErrorKind(data.Err)))
		t.logger.Warn("ошибка выполнения CopyFrom", fields...)
		return
	}

	if duration > slowQueryThreshold {
		t.logger.Warn("медленная операция CopyFrom", fields...)
		return
	}

	t.logger.Debug("операция CopyFrom завершена", fields...)
}

// TraceAcquireStart запоминает начало ожидания соединения из пула.
func (t *queryTracer) TraceAcquireStart(
	ctx context.Context,
	pool *pgxpool.Pool,
	data pgxpool.TraceAcquireStartData,
) context.Context {
	return context.WithValue(ctx, acquireTraceKey{}, acquireTraceData{startTime: time.Now()})
}

// TraceAcquireEnd записывает длительность ожидания соединения и безопасный тип ошибки.
func (t *queryTracer) TraceAcquireEnd(
	ctx context.Context,
	pool *pgxpool.Pool,
	data pgxpool.TraceAcquireEndData,
) {
	traceData, ok := ctx.Value(acquireTraceKey{}).(acquireTraceData)
	if !ok {
		return
	}
	duration := time.Since(traceData.startTime)
	fields := []zap.Field{zap.Duration("duration", duration)}
	if data.Err != nil {
		fields = append(fields, zap.String("error_kind", safeErrorKind(data.Err)))
		t.logger.Warn("ошибка получения соединения PostgreSQL", fields...)
		return
	}
	if duration > slowQueryThreshold {
		t.logger.Warn("долгое ожидание соединения PostgreSQL", fields...)
		return
	}
	t.logger.Debug("соединение PostgreSQL получено", fields...)
}

// safeErrorKind возвращает ограниченную категорию ошибки без её произвольного текста.
func safeErrorKind(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			return "postgres"
		}
		return "other"
	}
}

// extractOperation извлекает только ключевое слово SQL без параметров и имён объектов.
func extractOperation(sql string) string {
	trimmed := strings.TrimSpace(sql)
	if trimmed == "" {
		return "EMPTY"
	}
	parts := strings.Fields(trimmed)
	if len(parts) == 0 {
		return "UNKNOWN"
	}
	for _, character := range parts[0] {
		if character < 'A' || character > 'Z' {
			if character < 'a' || character > 'z' {
				return "UNKNOWN"
			}
		}
	}
	return strings.ToUpper(parts[0])
}

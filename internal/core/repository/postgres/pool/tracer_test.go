package core_postgres_pool

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestQueryTracerDoesNotLogSQLValuesOrErrorDetails(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	tracer := newQueryTracer(zap.New(core))
	ctx := tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 'secret-value'"})
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: &pgconn.PgError{
		Code:   "23505",
		Detail: "secret-value",
	}})

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("ожидалась одна запись, получено %d", len(entries))
	}
	entry := entries[0]
	if strings.Contains(fmt.Sprint(entry.ContextMap()), "secret-value") || strings.Contains(entry.Message, "secret-value") {
		t.Fatalf("секрет попал в лог: %+v", entry)
	}
	if entry.ContextMap()["sqlstate"] != "23505" || entry.ContextMap()["op"] != "SELECT" {
		t.Fatalf("потеряны безопасные диагностические поля: %+v", entry.ContextMap())
	}
}

func TestAcquireTracerDoesNotLogErrorText(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	tracer := newQueryTracer(zap.New(core))
	ctx := tracer.TraceAcquireStart(context.Background(), nil, pgxpool.TraceAcquireStartData{})
	tracer.TraceAcquireEnd(ctx, nil, pgxpool.TraceAcquireEndData{Err: fmt.Errorf("secret-value: %w", context.DeadlineExceeded)})

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("ожидалась одна запись, получено %d", len(entries))
	}
	if strings.Contains(fmt.Sprint(entries[0].ContextMap()), "secret-value") || entries[0].ContextMap()["error_kind"] != "deadline_exceeded" {
		t.Fatalf("небезопасная диагностика ожидания соединения: %+v", entries[0])
	}
}

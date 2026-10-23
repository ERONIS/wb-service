package core_postgres_transaction

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

type testBeginner struct {
	tx         *testTransaction
	beginCalls int
	options    pgx.TxOptions
}

func (b *testBeginner) BeginTx(_ context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	b.beginCalls++
	b.options = options
	return b.tx, nil
}

func TestPrepareTxWithStatementTimeout(t *testing.T) {
	tx := &testTransaction{}
	beginner := &testBeginner{tx: tx}
	options := pgx.TxOptions{IsoLevel: pgx.ReadCommitted}
	prepared, commit, rollback, err := PrepareTxWithStatementTimeout(context.Background(), beginner, 7, options)
	if err != nil {
		t.Fatalf("подготовка транзакции: %v", err)
	}
	defer rollback()
	if prepared != tx || beginner.beginCalls != 1 || beginner.options.IsoLevel != options.IsoLevel {
		t.Fatalf("неверная транзакция или параметры: begin=%d options=%+v", beginner.beginCalls, beginner.options)
	}
	if len(tx.execCalls) != 1 || tx.execCalls[0][0] != "statement_timeout" || tx.execCalls[0][1] != "7ms" {
		t.Fatalf("неверный локальный таймаут: %#v", tx.execCalls)
	}
	if err := commit(context.Background()); err != nil || tx.commitCalls != 1 {
		t.Fatalf("commit завершился неверно: err=%v calls=%d", err, tx.commitCalls)
	}
}

func TestPrepareTxRollsBackAfterTimeoutSetupError(t *testing.T) {
	setupErr := errors.New("ошибка настройки")
	tx := &testTransaction{execErr: setupErr}
	beginner := &testBeginner{tx: tx}
	prepared, commit, rollback, err := PrepareTxWithStatementTimeout(context.Background(), beginner, 7)
	if !errors.Is(err, setupErr) || prepared != nil || commit != nil || rollback != nil {
		t.Fatalf("ожидалась ошибка настройки без открытой транзакции: %v", err)
	}
	if tx.rollbackCalls != 1 {
		t.Fatalf("ошибка настройки не вызвала rollback: %d", tx.rollbackCalls)
	}
}

func TestPrepareTxRejectsInvalidTimeoutBeforeBegin(t *testing.T) {
	beginner := &testBeginner{tx: &testTransaction{}}
	_, _, _, err := PrepareTxWithStatementTimeout(context.Background(), beginner, -1)
	if err == nil || beginner.beginCalls != 0 {
		t.Fatalf("неверный таймаут открыл транзакцию: err=%v begin=%d", err, beginner.beginCalls)
	}
}

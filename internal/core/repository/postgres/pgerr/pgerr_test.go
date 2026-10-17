package pgerr

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPgErrorClassification(t *testing.T) {
	uniqueErr := fmt.Errorf("wrap: %w", &pgconn.PgError{
		Code:           CodeUniqueViolation,
		ConstraintName: "users_tg_id_key",
	})

	if !IsUniqueViolation(uniqueErr) {
		t.Errorf("ожидалась True для IsUniqueViolation")
	}
	if ConstraintName(uniqueErr) != "users_tg_id_key" {
		t.Errorf("ожидалось имя констрейнта 'users_tg_id_key', получено %q", ConstraintName(uniqueErr))
	}
	if SQLState(uniqueErr) != CodeUniqueViolation {
		t.Errorf("ожидался код 23505, получено %q", SQLState(uniqueErr))
	}

	fkErr := &pgconn.PgError{Code: CodeForeignKeyViolation}
	if !IsForeignKeyViolation(fkErr) {
		t.Errorf("ожидалась True для IsForeignKeyViolation")
	}

	checkErr := &pgconn.PgError{Code: CodeCheckViolation}
	if !IsCheckViolation(checkErr) {
		t.Errorf("ожидалась True для IsCheckViolation")
	}

	serialErr := &pgconn.PgError{Code: CodeSerializationFailure}
	if !IsSerializationFailure(serialErr) {
		t.Errorf("ожидалась True для IsSerializationFailure")
	}

	deadlockErr := &pgconn.PgError{Code: CodeDeadlockDetected}
	if !IsDeadlock(deadlockErr) {
		t.Errorf("ожидалась True для IsDeadlock")
	}

	immutableErr := &pgconn.PgError{Code: CodeObjectNotInPrerequisiteState}
	if !IsObjectNotInPrerequisiteState(immutableErr) {
		t.Errorf("ожидалась True для IsObjectNotInPrerequisiteState")
	}

	plainErr := fmt.Errorf("обычная ошибка")
	if IsUniqueViolation(plainErr) || IsDeadlock(plainErr) || SQLState(plainErr) != "" {
		t.Errorf("для обычной ошибки классификаторы должны возвращать false/пустоту")
	}
}

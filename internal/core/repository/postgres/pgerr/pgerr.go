package pgerr

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// Константы стандартных кодов ошибок PostgreSQL (SQLSTATE Class 23, 40, 55).
const (
	// CodeUniqueViolation — нарушение ограничения уникальности (23505).
	CodeUniqueViolation = "23505"

	// CodeForeignKeyViolation — нарушение ограничения внешнего ключа (23503).
	CodeForeignKeyViolation = "23503"

	// CodeCheckViolation — нарушение ограничения check (23514).
	CodeCheckViolation = "23514"

	// CodeSerializationFailure — ошибка сериализации транзакции (40001).
	CodeSerializationFailure = "40001"

	// CodeDeadlockDetected — обнаружен дедлок взаимной блокировки (40P01).
	CodeDeadlockDetected = "40P01"

	// CodeObjectNotInPrerequisiteState — состояние объекта не соответствует предусловию (55000).
	// Используется триггерами иммутабельности сервиса для защиты от мутаций.
	CodeObjectNotInPrerequisiteState = "55000"
)

// AsPgError извлекает *pgconn.PgError из цепочки ошибок err.
func AsPgError(err error) (*pgconn.PgError, bool) {
	if err == nil {
		return nil, false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr, true
	}
	return nil, false
}

// SQLState возвращает строковый код SQLSTATE ошибки PostgreSQL или пустую строку, если это не PgError.
func SQLState(err error) string {
	if pgErr, ok := AsPgError(err); ok {
		return pgErr.Code
	}
	return ""
}

// ConstraintName возвращает имя сработавшего ограничения базы данных или пустую строку.
func ConstraintName(err error) string {
	if pgErr, ok := AsPgError(err); ok {
		return pgErr.ConstraintName
	}
	return ""
}

// IsUniqueViolation проверяет, является ли ошибка нарушением уникальности (23505).
func IsUniqueViolation(err error) bool {
	return SQLState(err) == CodeUniqueViolation
}

// IsForeignKeyViolation проверяет, является ли ошибка нарушением внешнего ключа (23503).
func IsForeignKeyViolation(err error) bool {
	return SQLState(err) == CodeForeignKeyViolation
}

// IsCheckViolation проверяет, является ли ошибка нарушением check-ограничения (23514).
func IsCheckViolation(err error) bool {
	return SQLState(err) == CodeCheckViolation
}

// IsSerializationFailure проверяет, является ли ошибка конфликтом сериализации (40001).
func IsSerializationFailure(err error) bool {
	return SQLState(err) == CodeSerializationFailure
}

// IsDeadlock проверяет, является ли ошибка дедлоком транзакций (40P01).
func IsDeadlock(err error) bool {
	return SQLState(err) == CodeDeadlockDetected
}

// IsObjectNotInPrerequisiteState проверяет, является ли ошибка нарушением предусловия (55000),
// типичным для срабатывания защитных триггеров иммутабельности.
func IsObjectNotInPrerequisiteState(err error) bool {
	return SQLState(err) == CodeObjectNotInPrerequisiteState
}

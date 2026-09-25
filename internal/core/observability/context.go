package observability

import (
	"context"

	"go.uber.org/zap"
)

// Correlation carries only operation identifiers, never request payloads or credentials.
type Correlation struct {
	BatchID       int64
	TransferID    int64
	GroupTargetID int64
	ActionID      int64
}

type correlationKey struct{}

// WithCorrelation creates an independent child scope, preserving unspecified
// parent identifiers. A nil context is preserved for existing validation paths.
func WithCorrelation(ctx context.Context, scope Correlation) context.Context {
	if ctx == nil {
		return nil
	}
	parent, _ := ctx.Value(correlationKey{}).(Correlation)
	if scope.BatchID == 0 {
		scope.BatchID = parent.BatchID
	}
	if scope.TransferID == 0 {
		scope.TransferID = parent.TransferID
	}
	if scope.GroupTargetID == 0 {
		scope.GroupTargetID = parent.GroupTargetID
	}
	if scope.ActionID == 0 {
		scope.ActionID = parent.ActionID
	}
	return context.WithValue(ctx, correlationKey{}, scope)
}

// LoggerWithContext enriches leaf-operation logs using the caller's scope.
// It does not mutate the shared logger used by concurrent workers.
func LoggerWithContext(ctx context.Context, logger *zap.Logger) *zap.Logger {
	if ctx == nil || logger == nil {
		return logger
	}
	scope, ok := ctx.Value(correlationKey{}).(Correlation)
	if !ok {
		return logger
	}
	fields := make([]zap.Field, 0, 4)
	if scope.BatchID > 0 {
		fields = append(fields, zap.Int64("batch_id", scope.BatchID))
	}
	if scope.TransferID > 0 {
		fields = append(fields, zap.Int64("transfer_id", scope.TransferID))
	}
	if scope.GroupTargetID > 0 {
		fields = append(fields, zap.Int64("group_target_id", scope.GroupTargetID))
	}
	if scope.ActionID > 0 {
		fields = append(fields, zap.Int64("action_id", scope.ActionID))
	}
	return logger.With(fields...)
}

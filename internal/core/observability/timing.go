package observability

import (
	"time"

	"go.uber.org/zap"
)

const TimingEvent = "operation_timing"

// LogStarted makes in-flight work visible before its completion timing exists.
func LogStarted(logger *zap.Logger, component, operation string, fields ...zap.Field) {
	if logger == nil {
		return
	}
	base := []zap.Field{
		zap.String("event", "operation_started"),
		zap.String("component", component),
		zap.String("operation", operation),
	}
	logger.WithOptions(zap.AddCallerSkip(1)).Info("Timed operation started", append(base, fields...)...)
}

// Logger returns the configured logger or a no-op logger when logging is not
// wired (primarily useful for focused service tests).
func Logger(loggers ...*zap.Logger) *zap.Logger {
	for _, logger := range loggers {
		if logger != nil {
			return logger
		}
	}
	return zap.NewNop()
}

// LogTiming emits one consistently shaped, machine-filterable timing event.
func LogTiming(
	logger *zap.Logger,
	component string,
	operation string,
	startedAt time.Time,
	err error,
	fields ...zap.Field,
) {
	logTiming(logger, false, component, operation, startedAt, err, fields...)
}

// LogTimingDebug keeps fast background-loop timings at debug level. Slow calls
// (at least one second) remain visible at info; failures are always warnings.
func LogTimingDebug(
	logger *zap.Logger,
	component string,
	operation string,
	startedAt time.Time,
	err error,
	fields ...zap.Field,
) {
	logTiming(logger, true, component, operation, startedAt, err, fields...)
}

func logTiming(
	logger *zap.Logger,
	debug bool,
	component string,
	operation string,
	startedAt time.Time,
	err error,
	fields ...zap.Field,
) {
	if logger == nil {
		return
	}
	duration := time.Since(startedAt)
	base := []zap.Field{
		zap.String("event", TimingEvent),
		zap.String("component", component),
		zap.String("operation", operation),
		zap.Time("started_at", startedAt),
		zap.Duration("duration", duration),
		zap.Float64("duration_ms", float64(duration)/float64(time.Millisecond)),
	}
	logger = logger.WithOptions(zap.AddCallerSkip(2))
	base = append(base, fields...)
	if err != nil {
		base = append(base, zap.String("result", "error"), zap.Error(err))
		logger.Warn("Timed operation completed", base...)
		return
	}
	base = append(base, zap.String("result", "success"))
	if debug && duration < time.Second {
		logger.Debug("Timed operation completed", base...)
		return
	}
	logger.Info("Timed operation completed", base...)
}

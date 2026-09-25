package client

import (
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestRequestTraceLoggingLevels(t *testing.T) {
	tests := []struct {
		name          string
		errorCode     string
		limiterWait   time.Duration
		expectedLevel zapcore.Level
	}{
		{
			name:          "fast successful request logs at debug",
			errorCode:     "",
			limiterWait:   500 * time.Millisecond,
			expectedLevel: zapcore.DebugLevel,
		},
		{
			name:          "slow limiter wait successful request logs at info",
			errorCode:     "",
			limiterWait:   time.Second,
			expectedLevel: zapcore.InfoLevel,
		},
		{
			name:          "very slow limiter wait successful request logs at info",
			errorCode:     "",
			limiterWait:   3 * time.Second,
			expectedLevel: zapcore.InfoLevel,
		},
		{
			name:          "failed request logs at warn even with slow limiter wait",
			errorCode:     "wb_http_429",
			limiterWait:   2 * time.Second,
			expectedLevel: zapcore.WarnLevel,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			logger := zap.New(core)

			trace := &requestTrace{
				requestID:   "test-request-id",
				errorCode:   tc.errorCode,
				limiterWait: tc.limiterWait,
				startedAt:   time.Now(),
			}

			trace.log(logger)

			entries := logs.All()
			if len(entries) != 1 {
				t.Fatalf("expected 1 log entry, got %d", len(entries))
			}
			if entries[0].Level != tc.expectedLevel {
				t.Fatalf("expected level %v, got %v", tc.expectedLevel, entries[0].Level)
			}
			if entries[0].Message != "WB request completed" {
				t.Fatalf("unexpected message: %q", entries[0].Message)
			}
		})
	}
}

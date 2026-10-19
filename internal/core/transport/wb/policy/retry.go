package policy

// RetryMode определяет, разрешены ли автоматические повторы операции.
type RetryMode string

const (
	RetryModeNever    RetryMode = "never"
	RetryModeReadSafe RetryMode = "read_safe"
	// DefaultMaxReadAttempts задаёт число попыток для безопасных операций чтения.
	DefaultMaxReadAttempts = 3
)

func (mode RetryMode) String() string {
	return string(mode)
}

func (mode RetryMode) AllowsRetry() bool {
	return mode == RetryModeReadSafe
}

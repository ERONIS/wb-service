package policy

// RetryMode определяет, разрешены ли автоматические повторы операции.
type RetryMode string

const (
	RetryModeNever    RetryMode = "never"
	RetryModeReadSafe RetryMode = "read_safe"
)

func (mode RetryMode) String() string {
	return string(mode)
}

func (mode RetryMode) AllowsRetry() bool {
	return mode == RetryModeReadSafe
}

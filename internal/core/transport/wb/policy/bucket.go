package policy

import (
	"fmt"
	"time"
)

// BucketID — стабильный идентификатор rate-limit bucket операции.
type BucketID string

// BucketSpec описывает ограничения одного rate-limit bucket.
type BucketSpec struct {
	ID         BucketID
	Interval   time.Duration
	Burst      int
	MaxWaiters int
}

func (id BucketID) String() string {
	return string(id)
}

// Validate проверяет параметры rate-limit bucket.
func (spec BucketSpec) Validate() error {
	if spec.ID == "" {
		return fmt.Errorf("bucket ID is empty")
	}
	if spec.Interval <= 0 {
		return fmt.Errorf(
			"bucket %q interval must be positive",
			spec.ID,
		)
	}
	if spec.Burst <= 0 {
		return fmt.Errorf(
			"bucket %q burst must be positive",
			spec.ID,
		)
	}
	if spec.MaxWaiters <= 0 {
		return fmt.Errorf(
			"bucket %q max waiters must be positive",
			spec.ID,
		)
	}

	return nil
}

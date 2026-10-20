package policy

import (
	"fmt"
	"time"
)

// BucketSpec описывает ограничения одной группы запросов WB.
type BucketSpec struct {
	ID         BucketID
	Interval   time.Duration
	Burst      int
	MaxWaiters int
}

// ValidateBucketSpecs проверяет настройки всех групп перед созданием ограничителей.
func ValidateBucketSpecs(specs []BucketSpec) error {
	if len(specs) == 0 {
		return fmt.Errorf("rate limiter registry bucket specs are required")
	}

	seen := make(map[BucketID]struct{}, len(specs))
	for index, spec := range specs {
		if err := spec.validate(); err != nil {
			return fmt.Errorf("invalid bucket spec at index %d: %w", index, err)
		}
		if _, exists := seen[spec.ID]; exists {
			return fmt.Errorf("duplicate rate limiter bucket ID %q", spec.ID)
		}
		seen[spec.ID] = struct{}{}
	}

	return nil
}

func (spec BucketSpec) validate() error {
	if spec.ID == "" {
		return fmt.Errorf("bucket ID is empty")
	}
	if spec.Interval <= 0 {
		return fmt.Errorf("bucket %q interval must be positive", spec.ID)
	}
	if spec.Burst <= 0 {
		return fmt.Errorf("bucket %q burst must be positive", spec.ID)
	}
	if spec.MaxWaiters <= 0 {
		return fmt.Errorf("bucket %q max waiters must be positive", spec.ID)
	}

	return nil
}

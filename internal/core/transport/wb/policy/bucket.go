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

// BucketCatalog хранит проверенные настройки групп запросов.
type BucketCatalog struct {
	specs map[BucketID]BucketSpec
}

// NewBucketCatalog проверяет настройки и создаёт каталог групп запросов.
func NewBucketCatalog(specs []BucketSpec) (BucketCatalog, error) {
	if len(specs) == 0 {
		return BucketCatalog{}, fmt.Errorf("rate limiter registry bucket specs are required")
	}

	registeredSpecs := make(map[BucketID]BucketSpec, len(specs))
	for index, spec := range specs {
		if err := spec.validate(); err != nil {
			return BucketCatalog{}, fmt.Errorf("invalid bucket spec at index %d: %w", index, err)
		}
		if _, exists := registeredSpecs[spec.ID]; exists {
			return BucketCatalog{}, fmt.Errorf("duplicate rate limiter bucket ID %q", spec.ID)
		}
		registeredSpecs[spec.ID] = spec
	}

	return BucketCatalog{specs: registeredSpecs}, nil
}

func (catalog BucketCatalog) Spec(id BucketID) (BucketSpec, bool) {
	spec, exists := catalog.specs[id]
	return spec, exists
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

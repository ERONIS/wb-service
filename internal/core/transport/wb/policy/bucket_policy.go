package core_transport_wb_policy

import (
	"errors"
	"strings"
	"time"
)

type BucketPolicy struct {
	ID       BucketID
	Interval time.Duration
	Burst    int
}

func (policy BucketPolicy) Validate() error {
	if strings.TrimSpace(string(policy.ID)) == "" {
		return errors.New("bucket ID is empty")
	}
	if policy.Interval <= 0 {
		return errors.New("bucket interval must be positive")
	}
	if policy.Burst <= 0 {
		return errors.New("bucket burst must be positive")
	}

	return nil
}

package core_transport_wb_policy

import (
	"errors"
	"fmt"
	"strings"
)

type BucketID string

type RetryMode uint8

const (
	RetryDisabled RetryMode = iota
	RetrySafe
	RetryRateLimitOnly
)

type Operation struct {
	Name      string
	Method    string
	Path      string
	Buckets   []BucketID
	RetryMode RetryMode
}

func (operation Operation) Validate() error {
	if strings.TrimSpace(operation.Name) == "" {
		return errors.New("operation name is empty")
	}
	if strings.TrimSpace(operation.Method) == "" {
		return errors.New("operation method is empty")
	}
	if !strings.HasPrefix(operation.Path, "/") {
		return errors.New("operation path must start with /")
	}
	if err := ValidateBucketIDs(operation.Buckets); err != nil {
		return fmt.Errorf(
			"validate operation bucket IDs: %w",
			err,
		)
	}

	return nil
}

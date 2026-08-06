package core_transport_wb_policy

import (
	"errors"
	"strings"
)

func ValidateBucketID(
	bucketID BucketID,
) error {
	if strings.TrimSpace(string(bucketID)) == "" {
		return errors.New("bucket ID is empty")
	}

	return nil
}

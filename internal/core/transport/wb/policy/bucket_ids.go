package core_transport_wb_policy

import (
	"errors"
	"fmt"
	"strings"
)

func ValidateBucketIDs(
	bucketIDs []BucketID,
) error {
	if len(bucketIDs) == 0 {
		return errors.New("bucket IDs are empty")
	}

	seen := make(
		map[BucketID]struct{},
		len(bucketIDs),
	)

	for _, bucketID := range bucketIDs {
		if strings.TrimSpace(string(bucketID)) == "" {
			return errors.New("bucket ID is empty")
		}

		if _, exists := seen[bucketID]; exists {
			return fmt.Errorf(
				"duplicate bucket ID %q",
				bucketID,
			)
		}

		seen[bucketID] = struct{}{}
	}

	return nil
}

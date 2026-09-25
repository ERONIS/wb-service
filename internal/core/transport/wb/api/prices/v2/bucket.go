package v2

import (
	"time"

	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

var bucketSpecs = []policy.BucketSpec{
	mustBucketSpec(policy.BucketSpec{
		ID:         bucketIDPricesRead,
		Interval:   600 * time.Millisecond,
		Burst:      5,
		MaxWaiters: 256,
	}),
}

func BucketSpecs() []policy.BucketSpec {
	result := make([]policy.BucketSpec, len(bucketSpecs))
	copy(result, bucketSpecs)
	return result
}

func mustBucketSpec(spec policy.BucketSpec) policy.BucketSpec {
	if err := spec.Validate(); err != nil {
		panic(err)
	}
	return spec
}

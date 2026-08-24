package v1

import (
	"time"

	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

const maxWaitersPerBucket = 256

var generalBucketSpecs = []policy.BucketSpec{
	mustBucketSpec(policy.BucketSpec{
		ID:         bucketIDSellerInfo,
		Interval:   time.Minute,
		Burst:      10,
		MaxWaiters: maxWaitersPerBucket,
	}),
}

func BucketSpecs() []policy.BucketSpec {
	specs := make([]policy.BucketSpec, len(generalBucketSpecs))
	copy(specs, generalBucketSpecs)
	return specs
}

func mustBucketSpec(spec policy.BucketSpec) policy.BucketSpec {
	if err := spec.Validate(); err != nil {
		panic(err)
	}
	return spec
}

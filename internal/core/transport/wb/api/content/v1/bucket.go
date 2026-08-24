package v1

import (
	"time"

	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

const maxWaitersPerBucket = 256

var contentBucketSpecs = []policy.BucketSpec{
	mustBucketSpec(policy.BucketSpec{
		ID:         bucketIDContentPing,
		Interval:   10 * time.Second,
		Burst:      3,
		MaxWaiters: maxWaitersPerBucket,
	}),
	mustBucketSpec(policy.BucketSpec{
		ID:         bucketIDContentCommon,
		Interval:   600 * time.Millisecond,
		Burst:      5,
		MaxWaiters: maxWaitersPerBucket,
	}),
	mustBucketSpec(policy.BucketSpec{
		ID:         bucketIDBrands,
		Interval:   time.Second,
		Burst:      5,
		MaxWaiters: maxWaitersPerBucket,
	}),
	mustBucketSpec(policy.BucketSpec{
		ID:         bucketIDCharacteristics,
		Interval:   600 * time.Millisecond,
		Burst:      5,
		MaxWaiters: maxWaitersPerBucket,
	}),
	mustBucketSpec(policy.BucketSpec{
		ID:         bucketIDCardsLimitsAndErrors,
		Interval:   600 * time.Millisecond,
		Burst:      5,
		MaxWaiters: maxWaitersPerBucket,
	}),
	mustBucketSpec(policy.BucketSpec{
		ID:         bucketIDCardsList,
		Interval:   600 * time.Millisecond,
		Burst:      5,
		MaxWaiters: maxWaitersPerBucket,
	}),
	mustBucketSpec(policy.BucketSpec{
		ID:         bucketIDCardsUpload,
		Interval:   6 * time.Second,
		Burst:      5,
		MaxWaiters: maxWaitersPerBucket,
	}),
	mustBucketSpec(policy.BucketSpec{
		ID:         bucketIDCardsUploadAdd,
		Interval:   6 * time.Second,
		Burst:      5,
		MaxWaiters: maxWaitersPerBucket,
	}),
	mustBucketSpec(policy.BucketSpec{
		ID:         bucketIDMediaFiles,
		Interval:   600 * time.Millisecond,
		Burst:      5,
		MaxWaiters: maxWaitersPerBucket,
	}),
}

// BucketSpecs возвращает независимую копию политик Content API v1.
func BucketSpecs() []policy.BucketSpec {
	specs := make(
		[]policy.BucketSpec,
		len(contentBucketSpecs),
	)
	copy(specs, contentBucketSpecs)

	return specs
}

func mustBucketSpec(
	spec policy.BucketSpec,
) policy.BucketSpec {
	if err := spec.Validate(); err != nil {
		panic(err)
	}

	return spec
}

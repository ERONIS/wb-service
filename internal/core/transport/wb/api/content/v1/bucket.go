package v1

import (
	"time"

	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

const maxWaitersPerBucket = 256

var contentBuckets = mustBucketCatalog([]policy.BucketSpec{
	contentBucket(bucketIDContentPing, 10*time.Second, 3),
	contentBucket(bucketIDContentCommon, 600*time.Millisecond, 5),
	contentBucket(bucketIDBrands, time.Second, 5),
	contentBucket(bucketIDCharacteristics, 600*time.Millisecond, 5),
	contentBucket(bucketIDCardsLimitsAndErrors, 600*time.Millisecond, 5),
	contentBucket(bucketIDCardsList, 600*time.Millisecond, 5),
	contentBucket(bucketIDCardsUpload, 6*time.Second, 5),
	contentBucket(bucketIDCardsUploadAdd, 6*time.Second, 5),
	contentBucket(bucketIDMediaFiles, 600*time.Millisecond, 5),
})

// BucketCatalog возвращает проверенные настройки лимитов Content API v1.
func BucketCatalog() policy.BucketCatalog {
	return contentBuckets
}

func contentBucket(id policy.BucketID, interval time.Duration, burst int) policy.BucketSpec {
	return policy.BucketSpec{
		ID:         id,
		Interval:   interval,
		Burst:      burst,
		MaxWaiters: maxWaitersPerBucket,
	}
}

func mustBucketCatalog(specs []policy.BucketSpec) policy.BucketCatalog {
	catalog, err := policy.NewBucketCatalog(specs)
	if err != nil {
		panic(err)
	}
	return catalog
}

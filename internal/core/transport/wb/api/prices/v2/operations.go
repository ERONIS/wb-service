package v2

import (
	"net/http"

	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

const (
	operationIDGoodsByNM policy.OperationID = "prices.goods.by-nm"
	bucketIDPricesRead   policy.BucketID    = "prices_read"
)

var goodsByNMOperation = mustOperation(policy.OperationSpec{
	ID:               operationIDGoodsByNM,
	Method:           http.MethodPost,
	Path:             "/api/v2/list/goods/filter",
	BucketID:         bucketIDPricesRead,
	Kind:             policy.OperationKindRead,
	RetryMode:        policy.RetryModeReadSafe,
	SuccessStatuses:  []int{http.StatusOK},
	RequestMode:      policy.BodyModeJSON,
	ResponseMode:     policy.BodyModeJSON,
	MaxRequestBytes:  64 * 1024,
	MaxResponseBytes: 16 * 1024 * 1024,
})

func GoodsByNMOperation() policy.Operation {
	return goodsByNMOperation
}

func mustOperation(spec policy.OperationSpec) policy.Operation {
	operation, err := policy.NewOperation(spec)
	if err != nil {
		panic(err)
	}
	return operation
}

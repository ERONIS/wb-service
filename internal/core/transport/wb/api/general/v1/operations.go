package v1

import (
	"net/http"

	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

const (
	operationIDSellerInfo policy.OperationID = "general.seller-info"
	bucketIDSellerInfo    policy.BucketID    = "general_seller_info"
)

const maxSellerInfoResponseBytes = 16 * 1024

var sellerInfoOperation = mustOperation(policy.OperationSpec{
	ID:               operationIDSellerInfo,
	Method:           http.MethodGet,
	Path:             "/api/v1/seller-info",
	BucketID:         bucketIDSellerInfo,
	Kind:             policy.OperationKindRead,
	RetryMode:        policy.RetryModeReadSafe,
	SuccessStatuses:  []int{http.StatusOK},
	RequestMode:      policy.BodyModeNone,
	ResponseMode:     policy.BodyModeJSON,
	MaxRequestBytes:  0,
	MaxResponseBytes: maxSellerInfoResponseBytes,
})

func SellerInfoOperation() policy.Operation {
	return sellerInfoOperation
}

func mustOperation(spec policy.OperationSpec) policy.Operation {
	operation, err := policy.NewOperation(spec)
	if err != nil {
		panic(err)
	}
	return operation
}

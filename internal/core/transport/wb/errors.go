package wb

import (
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
)

var ErrCabinetNotFound = fmt.Errorf("WB cabinet: %w", core_errors.ErrNotFound)

type DeliveryState = client.DeliveryState
type ClassifiedError = client.ClassifiedError

const (
	NotDispatched    = client.NotDispatched
	ResponseReceived = client.ResponseReceived
	UnknownDelivery  = client.UnknownDelivery

	ErrorCodeInvalidRequest      = client.ErrorCodeInvalidRequest
	ErrorCodeInvalidResultTarget = client.ErrorCodeInvalidResultTarget
	ErrorCodeInvalidResponse     = client.ErrorCodeInvalidResponse
	ErrorCodeRateLimited         = client.ErrorCodeRateLimited
	ErrorCodeRetryInterrupted    = client.ErrorCodeRetryInterrupted
	ErrorCodeUnexpectedStatus    = client.ErrorCodeUnexpectedStatus
	ErrorCodeTransport           = client.ErrorCodeTransport
)

package wb

import (
	"errors"

	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
)

var ErrCabinetNotFound = errors.New("WB cabinet not found")

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

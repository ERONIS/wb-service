package publication

import (
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	"github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

type TransferID = model.TransferID
type Digest = model.Digest
type CabinetID = model.CabinetID
type ResultClass = model.ResultClass

const (
	ResultSuccess       = model.ResultSuccess
	ResultSkipped       = model.ResultSkipped
	ResultRejected      = model.ResultRejected
	ResultPartial       = model.ResultPartial
	ResultUnresolved    = model.ResultUnresolved
	ResultInternalError = model.ResultInternalError
)

func invalidTransfer(message string) error {
	return core_errors.InvalidArgument("invalid transfer: %s", message)
}

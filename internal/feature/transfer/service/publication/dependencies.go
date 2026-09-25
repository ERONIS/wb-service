package publication

import (
	"fmt"

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
	return fmt.Errorf("invalid transfer: %s", message)
}

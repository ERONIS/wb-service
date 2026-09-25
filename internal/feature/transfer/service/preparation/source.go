package preparation

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	"github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

type PreparationSourceItem struct {
	TransferItemID int64
	Position       int
	VendorCode     string
	Card           cardpipeline.Card
}

type PreparationSource struct {
	TransferID    model.TransferID
	GroupTargetID int64
	SourceGroupID int64
	TargetID      int64
	CabinetID     model.CabinetID
	Items         []PreparationSourceItem
}

func (source PreparationSource) Validate() error {
	if source.TransferID <= 0 || source.GroupTargetID <= 0 ||
		source.SourceGroupID <= 0 || source.TargetID <= 0 ||
		strings.TrimSpace(string(source.CabinetID)) != string(source.CabinetID) ||
		source.CabinetID == "" || len(source.Items) == 0 {
		return errors.New("transfer preparation source is invalid")
	}
	seenItems := make(map[int64]struct{}, len(source.Items))
	for index, item := range source.Items {
		if item.TransferItemID <= 0 || item.Position <= 0 ||
			strings.TrimSpace(item.VendorCode) != item.VendorCode ||
			item.VendorCode == "" || item.Card.Variant.VendorCode != item.VendorCode {
			return fmt.Errorf("transfer preparation source item at index %d is invalid", index)
		}
		if index > 0 && item.Position <= source.Items[index-1].Position {
			return errors.New("transfer preparation source positions are not increasing")
		}
		if _, exists := seenItems[item.TransferItemID]; exists {
			return errors.New("transfer preparation source item is duplicated")
		}
		seenItems[item.TransferItemID] = struct{}{}
	}
	return nil
}

package preparation

import (
	"errors"
	"fmt"
	"strings"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
)

type PreparationSourceItem struct {
	Position int
	Card     cardpipeline.Card
}

type PreparationSource struct {
	SellerID core_domain.SellerID
	Items    []PreparationSourceItem
}

// PreparationPendingGroup описывает целевую группу карточек, ожидающую расчёта подготовки.
type PreparationPendingGroup struct {
	GroupTargetID int64
	SourceGroupID int64
	TargetID      int64
}

func (source PreparationSource) Validate() error {
	if strings.TrimSpace(string(source.SellerID)) != string(source.SellerID) ||
		source.SellerID == "" || len(source.Items) == 0 {
		return errors.New("transfer preparation source is invalid")
	}
	for index, item := range source.Items {
		if item.Position <= 0 ||
			strings.TrimSpace(item.Card.Variant.VendorCode) != item.Card.Variant.VendorCode ||
			item.Card.Variant.VendorCode == "" {
			return fmt.Errorf("transfer preparation source item at index %d is invalid", index)
		}
		if index > 0 && item.Position <= source.Items[index-1].Position {
			return errors.New("transfer preparation source positions are not increasing")
		}
	}
	return nil
}

// Validate проверяет корректность идентификаторов и кабинета целевой группы.
func (group PreparationPendingGroup) Validate() error {
	if group.GroupTargetID <= 0 || group.SourceGroupID <= 0 || group.TargetID <= 0 {
		return errors.New("transfer preparation pending group is invalid")
	}
	return nil
}

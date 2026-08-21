package transfer_service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
)

const MaxPreparingPageSize = 1000

type PreparationSourceItem struct {
	TransferItemID int64
	Position       int
	VendorCode     string
	Card           cardimport_service.AggregatedCard
}

type PreparationSource struct {
	TransferID    TransferID
	GroupTargetID int64
	SourceGroupID int64
	TargetID      int64
	CabinetID     CabinetID
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
			item.VendorCode == "" ||
			item.Card.Variant.VendorCode != item.VendorCode {
			return fmt.Errorf(
				"transfer preparation source item at index %d is invalid",
				index,
			)
		}
		if index > 0 && item.Position <= source.Items[index-1].Position {
			return errors.New(
				"transfer preparation source positions are not increasing",
			)
		}
		if _, exists := seenItems[item.TransferItemID]; exists {
			return errors.New("transfer preparation source item is duplicated")
		}
		seenItems[item.TransferItemID] = struct{}{}
	}
	return nil
}

func (service *Service) ListPreparing(
	ctx context.Context,
	afterID TransferID,
	limit int,
) ([]Transfer, error) {
	if ctx == nil {
		return nil, errors.New("list preparing transfers: context is nil")
	}
	if afterID < 0 || limit <= 0 || limit > MaxPreparingPageSize {
		return nil, core_errors.ErrInvalidArgument
	}
	return service.repository.ListPreparing(ctx, afterID, limit)
}

func (service *Service) BeginPreparation(
	ctx context.Context,
	transferID TransferID,
) error {
	if ctx == nil {
		return errors.New("begin transfer preparation: context is nil")
	}
	if transferID <= 0 {
		return core_errors.ErrInvalidArgument
	}
	if err := service.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			return service.repository.BeginPreparation(ctx, tx, transferID)
		},
	); err != nil {
		return fmt.Errorf("begin transfer preparation: %w", err)
	}
	return nil
}

func (service *Service) LoadPreparationSource(
	ctx context.Context,
	transferID TransferID,
	groupTargetID int64,
) (PreparationSource, error) {
	if ctx == nil {
		return PreparationSource{}, errors.New(
			"load transfer preparation source: context is nil",
		)
	}
	if transferID <= 0 || groupTargetID <= 0 {
		return PreparationSource{}, core_errors.ErrInvalidArgument
	}
	source, err := service.repository.LoadPreparationSource(
		ctx,
		transferID,
		groupTargetID,
	)
	if err != nil {
		return PreparationSource{}, err
	}
	if err := source.Validate(); err != nil {
		return PreparationSource{}, fmt.Errorf(
			"validate transfer preparation source: %w",
			err,
		)
	}
	return source, nil
}

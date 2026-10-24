package cardpublication_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_model "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/model"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

// InsertActionObservation сохраняет снимок наблюдения (preflight или recheck).
func (repository *Repository) InsertActionObservation(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_model.TransferID,
	kind string,
	observation cardpublication_model.ObservationDraft,
) (int64, error) {
	if tx == nil || transferID <= 0 ||
		(kind != "targeted_recheck" && kind != "post_submission") {
		return 0, errors.New("insert action observation command is invalid")
	}
	const insert = `
		INSERT INTO wb.publication_observations (
			transfer_id, target_id, seller_id, kind,
			normal_count, trash_count, snapshot, observed_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id;
	`
	var observationID int64
	err := tx.QueryRow(
		ctx,
		insert,
		transferID,
		observation.TargetID,
		observation.SellerID,
		kind,
		observation.NormalCount,
		observation.TrashCount,
		observation.Payload,
		observation.ObservedAt,
	).Scan(&observationID)
	if err != nil {
		return 0, fmt.Errorf("insert publication action observation: %w", err)
	}
	return observationID, nil
}

// equalBytes сравнивает два среза байт на идентичность длины и содержимого.
func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// nullablePositive возвращает целое положительное число или nil для SQL-параметров.
func nullablePositive(value int64) any {
	if value > 0 {
		return value
	}
	return nil
}

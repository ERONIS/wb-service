package transfer_postgres_repository_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardprepare_model "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/model"
	transfer_postgres_repository "github.com/ERONIS/wb-service/internal/feature/transfer/repository/postgres"
	transfer_preparation "github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"
)

func TestPostgres_LoadPublicationPlanningSourceIncludesProposal(t *testing.T) {
	pool, uow := getTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	transferID, groupTargetID, _, targetID, _ := setupTestTransferData(t, ctx, pool)
	repository := transfer_postgres_repository.New(pool)

	const subjectID int64 = 12345
	request := contentapi.UploadCardsRequest{{
		SubjectID: subjectID,
		Variants:  []contentapi.UploadCard{{VendorCode: "ART-1"}},
	}}
	requestPayload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	snapshot := cardprepare_model.CatalogSnapshot{
		Limits:     contentapi.CardsLimits{FreeLimits: 100},
		ObservedAt: now,
	}
	snapshotPayload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	command := transfer_preparation.ApplyPreparationResultCommand{
		TransferID:    transferID,
		GroupTargetID: groupTargetID,
		Status:        transfer_preparation.PreparationResultSucceeded,
		OutcomeCode:   "prepared",
		StartedAt:     now.Add(-time.Second),
		FinishedAt:    now,
		Artifact: &transfer_preparation.PreparationArtifact{
			SubjectID:        subjectID,
			RequestPayload:   requestPayload,
			MetadataSnapshot: snapshotPayload,
			LimitsFree:       snapshot.Limits.FreeLimits,
			LimitsPaid:       snapshot.Limits.PaidLimits,
			LimitsObservedAt: now,
		},
	}
	if err := uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		return repository.ApplyPreparationResult(ctx, tx, command)
	}); err != nil {
		t.Fatalf("сохранение результата подготовки: %v", err)
	}

	source, err := repository.LoadPublicationPlanningSource(ctx, transferID)
	if err != nil {
		t.Fatalf("чтение источника планирования: %v", err)
	}
	if len(source.Groups) != 1 {
		t.Fatalf("ожидалась одна подготовленная группа, получено %d", len(source.Groups))
	}
	group := source.Groups[0]
	if group.GroupTargetID != groupTargetID || group.TargetID != targetID ||
		group.Proposal.SubjectID != subjectID || len(group.Members) != 1 ||
		group.Members[0].VendorCode != "ART-1" || group.Proposal.Validate() != nil {
		t.Fatalf("источник планирования не совпадает с подготовленным артефактом: %+v", group)
	}
}

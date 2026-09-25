package cardprepare_postgres_repository

import (
	"encoding/json"
	"testing"
	"time"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardprepare_service "github.com/ERONIS/wb-service/internal/feature/cardprepare/service"
	cardprepare_model "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/model"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

func TestDecodeStoredProposalPreservesOrderedBatchMembers(t *testing.T) {
	t.Parallel()

	observedAt := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	snapshot := cardprepare_service.CatalogSnapshot{
		Limits:     contentapi.CardsLimits{FreeLimits: 10, PaidLimits: 2},
		ObservedAt: observedAt,
	}
	metadataDigest, err := cardprepare_model.DigestJSON("cardprepare-metadata:v1", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	semanticDigest := cardprepare_service.Digest{1}
	request := contentapi.UploadCardsRequest{{
		SubjectID: 777,
		Variants: []contentapi.UploadCard{
			{VendorCode: "SKU-1"},
			{VendorCode: "SKU-2"},
		},
	}}
	requestPayload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	proposalRoot := cardprepare_model.DigestProposal(
		semanticDigest,
		metadataDigest,
		requestPayload,
	)
	snapshotPayload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	stored := cardprepare_service.StoredProposal{
		PreparationID: 1,
		SourceGroupID: 2,
		TargetID:      3,
		CabinetID:     "cabinet",
	}
	stored.Proposal.SubjectID = 777
	stored.Proposal.Limits = snapshot.Limits
	stored.Proposal.LimitsObservedAt = observedAt
	query := cardprepare_service.ProposalQuery{
		TransferID:         transfer_service.TransferID(4),
		GroupTargetID:      5,
		PreparationGroupID: 6,
		ProposalRoot:       proposalRoot,
	}

	err = decodeStoredProposal(
		&stored,
		query,
		semanticDigest[:],
		metadataDigest[:],
		proposalRoot[:],
		requestPayload,
		snapshotPayload,
		[]int64{11, 12},
		[]int64{1, 2},
		[]string{"SKU-1", "SKU-2"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Members) != 2 || stored.Members[0].TransferItemID != 11 ||
		stored.Members[1].TransferItemID != 12 || stored.Members[1].VendorCode != "SKU-2" {
		t.Fatalf("unexpected decoded members: %#v", stored.Members)
	}
}

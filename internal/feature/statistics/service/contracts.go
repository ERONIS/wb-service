package statistics_service

import (
	"context"
	"time"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardpublication_resolution "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/resolution"
)

type Reader interface {
	ListOperations(context.Context, OperationFilter) ([]OperationRow, error)
	GetOperation(context.Context, int64) (OperationDetails, error)
	GetAction(context.Context, int64) (ActionDetails, error)
	ListCabinetProgress(context.Context, int64) ([]CabinetProgressRow, error)
	ListCabinetTasks(context.Context, CabinetTaskFilter) (CabinetTaskPage, error)
	AggregateTransfers(context.Context, AggregateFilter) (TransferTotals, error)
	AggregateItems(context.Context, AggregateFilter) (ItemTotals, error)
	AggregateActions(context.Context, AggregateFilter) (ActionTotals, error)
	AggregateErrors(context.Context, AggregateFilter) ([]ErrorGroup, error)
	ListAttention(context.Context, AttentionFilter) ([]AttentionRow, error)
}

type VerifiedCardResolver interface {
	Resolve(context.Context, cardpublication_resolution.VerifiedCardCommand, cardpublication_resolution.VerifiedCardEvidence) error
}

type CardVerificationInfo struct {
	Card       contentapi.Card
	ObservedAt time.Time
	NMID       int64
	IMTID      int64
	SubjectID  int64
	Title      string
	Brand      string
}

type CardVerifier interface {
	FindCardByVendorCode(ctx context.Context, sellerID string, vendorCode string) (*CardVerificationInfo, error)
}

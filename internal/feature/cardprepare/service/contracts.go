package cardprepare_service

import (
	"context"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

type CabinetID string

type PreparationRepository interface {
	StartPreparation(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command StartPreparationCommand,
	) error

	ListPreparationWork(
		ctx context.Context,
		transferID transfer_service.TransferID,
	) ([]PreparationWork, error)

	SavePreparationResult(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command SavePreparationResultCommand,
	) (PreparationWork, error)
}

type TransferSource interface {
	ListPreparing(
		ctx context.Context,
		afterID transfer_service.TransferID,
		limit int,
	) ([]transfer_service.Transfer, error)

	BeginPreparation(
		ctx context.Context,
		transferID transfer_service.TransferID,
	) error

	LoadPreparationSource(
		ctx context.Context,
		transferID transfer_service.TransferID,
		groupTargetID int64,
	) (transfer_service.PreparationSource, error)
}

type TransferResultApplier interface {
	Apply(
		ctx context.Context,
		command transfer_service.ApplyPreparationResultCommand,
	) error
}

// CatalogTransport performs exactly one raw WB read per method. It does not
// paginate or interpret ResponseMeta.
type CatalogTransport interface {
	CardsLimits(
		ctx context.Context,
		cabinetID CabinetID,
	) (contentapi.CardsLimitsResponse, error)

	Subjects(
		ctx context.Context,
		cabinetID CabinetID,
		query contentapi.SubjectsQuery,
	) (contentapi.SubjectsResponse, error)

	SubjectCharacteristics(
		ctx context.Context,
		cabinetID CabinetID,
		subjectID int64,
		query contentapi.SubjectCharacteristicsQuery,
	) (contentapi.SubjectCharacteristicsResponse, error)

	Brands(
		ctx context.Context,
		cabinetID CabinetID,
		query contentapi.BrandsQuery,
	) (contentapi.BrandsResponse, error)

	Colors(
		ctx context.Context,
		cabinetID CabinetID,
		query contentapi.DirectoryQuery,
	) (contentapi.DirectoryColorsResponse, error)

	Kinds(
		ctx context.Context,
		cabinetID CabinetID,
		query contentapi.DirectoryQuery,
	) (contentapi.DirectoryKindsResponse, error)

	Countries(
		ctx context.Context,
		cabinetID CabinetID,
		query contentapi.DirectoryQuery,
	) (contentapi.DirectoryCountriesResponse, error)

	Seasons(
		ctx context.Context,
		cabinetID CabinetID,
		query contentapi.DirectoryQuery,
	) (contentapi.DirectorySeasonsResponse, error)

	VAT(
		ctx context.Context,
		cabinetID CabinetID,
		query contentapi.DirectoryQuery,
	) (contentapi.DirectoryVATResponse, error)

	TNVED(
		ctx context.Context,
		cabinetID CabinetID,
		query contentapi.DirectoryTNVEDQuery,
	) (contentapi.DirectoryTNVEDResponse, error)
}

// ProposalReader is the only cardprepare data surface exposed to downstream
// features. Callers must present the complete immutable reference from the
// stored proposal reference.
type ProposalReader interface {
	LoadProposal(
		ctx context.Context,
		query ProposalQuery,
	) (StoredProposal, error)
}

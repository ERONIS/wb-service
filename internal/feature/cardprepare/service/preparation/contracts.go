package preparation

import (
	"context"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

// CatalogTransport performs exactly one raw WB read per method. It does not
// paginate or interpret ResponseMeta.
type CatalogTransport interface {
	CardsLimits(context.Context, CabinetID) (contentapi.CardsLimitsResponse, error)
	Subjects(
		context.Context,
		CabinetID,
		contentapi.SubjectsQuery,
	) (contentapi.SubjectsResponse, error)
	SubjectCharacteristics(
		context.Context,
		CabinetID,
		int64,
		contentapi.SubjectCharacteristicsQuery,
	) (contentapi.SubjectCharacteristicsResponse, error)
	Brands(
		context.Context,
		CabinetID,
		contentapi.BrandsQuery,
	) (contentapi.BrandsResponse, error)
	Colors(
		context.Context,
		CabinetID,
		contentapi.DirectoryQuery,
	) (contentapi.DirectoryColorsResponse, error)
	Kinds(
		context.Context,
		CabinetID,
		contentapi.DirectoryQuery,
	) (contentapi.DirectoryKindsResponse, error)
	Countries(
		context.Context,
		CabinetID,
		contentapi.DirectoryQuery,
	) (contentapi.DirectoryCountriesResponse, error)
	Seasons(
		context.Context,
		CabinetID,
		contentapi.DirectoryQuery,
	) (contentapi.DirectorySeasonsResponse, error)
	VAT(
		context.Context,
		CabinetID,
		contentapi.DirectoryQuery,
	) (contentapi.DirectoryVATResponse, error)
	TNVED(
		context.Context,
		CabinetID,
		contentapi.DirectoryTNVEDQuery,
	) (contentapi.DirectoryTNVEDResponse, error)
}

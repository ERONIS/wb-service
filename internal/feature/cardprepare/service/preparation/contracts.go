package preparation

import (
	"context"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

type CatalogTransport interface {
	CardsLimits(context.Context, core_domain.SellerID) (contentapi.CardsLimitsResponse, error)
	Subjects(
		context.Context,
		core_domain.SellerID,
		contentapi.SubjectsQuery,
	) (contentapi.SubjectsResponse, error)
	SubjectCharacteristics(
		context.Context,
		core_domain.SellerID,
		int64,
		contentapi.SubjectCharacteristicsQuery,
	) (contentapi.SubjectCharacteristicsResponse, error)
	Brands(
		context.Context,
		core_domain.SellerID,
		contentapi.BrandsQuery,
	) (contentapi.BrandsResponse, error)
	Colors(
		context.Context,
		core_domain.SellerID,
		contentapi.DirectoryQuery,
	) (contentapi.DirectoryColorsResponse, error)
	Kinds(
		context.Context,
		core_domain.SellerID,
		contentapi.DirectoryQuery,
	) (contentapi.DirectoryKindsResponse, error)
	Countries(
		context.Context,
		core_domain.SellerID,
		contentapi.DirectoryQuery,
	) (contentapi.DirectoryCountriesResponse, error)
	Seasons(
		context.Context,
		core_domain.SellerID,
		contentapi.DirectoryQuery,
	) (contentapi.DirectorySeasonsResponse, error)
	VAT(
		context.Context,
		core_domain.SellerID,
		contentapi.DirectoryQuery,
	) (contentapi.DirectoryVATResponse, error)
	TNVED(
		context.Context,
		core_domain.SellerID,
		contentapi.DirectoryTNVEDQuery,
	) (contentapi.DirectoryTNVEDResponse, error)
}

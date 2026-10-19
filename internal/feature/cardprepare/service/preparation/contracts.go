package preparation

import (
	"context"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

type CatalogTransport interface {
	CardsLimits(context.Context, SellerID) (contentapi.CardsLimitsResponse, error)
	Subjects(
		context.Context,
		SellerID,
		contentapi.SubjectsQuery,
	) (contentapi.SubjectsResponse, error)
	SubjectCharacteristics(
		context.Context,
		SellerID,
		int64,
		contentapi.SubjectCharacteristicsQuery,
	) (contentapi.SubjectCharacteristicsResponse, error)
	Brands(
		context.Context,
		SellerID,
		contentapi.BrandsQuery,
	) (contentapi.BrandsResponse, error)
	Colors(
		context.Context,
		SellerID,
		contentapi.DirectoryQuery,
	) (contentapi.DirectoryColorsResponse, error)
	Kinds(
		context.Context,
		SellerID,
		contentapi.DirectoryQuery,
	) (contentapi.DirectoryKindsResponse, error)
	Countries(
		context.Context,
		SellerID,
		contentapi.DirectoryQuery,
	) (contentapi.DirectoryCountriesResponse, error)
	Seasons(
		context.Context,
		SellerID,
		contentapi.DirectoryQuery,
	) (contentapi.DirectorySeasonsResponse, error)
	VAT(
		context.Context,
		SellerID,
		contentapi.DirectoryQuery,
	) (contentapi.DirectoryVATResponse, error)
	TNVED(
		context.Context,
		SellerID,
		contentapi.DirectoryTNVEDQuery,
	) (contentapi.DirectoryTNVEDResponse, error)
}
